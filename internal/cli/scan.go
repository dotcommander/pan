package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/scan"
)

// ScanCmd groups bounded repository inventory and discovery surfaces.
type ScanCmd struct {
	Overview  OverviewCmd  `cmd:"" help:"Repository and symbol counts."`
	Files     FilesCmd     `cmd:"" help:"File inventory."`
	Symbols   SymbolsCmd   `cmd:"" help:"Symbol inventory."`
	Risks     RisksCmd     `cmd:"" help:"Risk-ranked review queue."`
	Surface   SurfaceCmd   `cmd:"" help:"Public surface inventory."`
	Effects   EffectsCmd   `cmd:"" help:"Side-effect and trust boundaries."`
	Hygiene   HygieneCmd   `cmd:"" help:"Git hygiene inventory."`
	Changes   ChangesCmd   `cmd:"" help:"Change evidence over history."`
	Orphans   OrphansCmd   `cmd:"" help:"Lexical zero-reference symbol candidates."`
	Inventory InventoryCmd `cmd:"" help:"Boundary-owner inventory."`
	Doctor    DoctorCmd    `cmd:"" help:"Analyzer health check."`
}

// validateTop rejects negative --top values; zero lists all entries.
func validateTop(top int) error {
	if top < 0 {
		return errors.New("--top must not be negative")
	}
	return nil
}

// OverviewCmd is `pan scan overview`.
type OverviewCmd struct{}

// Run executes `pan scan overview`.
func (OverviewCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, report, err := deps.App.Overview(ctx, root.Repo)
	if err != nil {
		return err
	}
	if root.Format == formatJSON || !isTerminalWriter(deps.Out) {
		return emit(kctx, root, deps, snap, report)
	}
	return writeOverview(deps.Out, snap, report)
}

// FilesCmd is `pan scan files`.
type FilesCmd struct {
	Top int `name:"top" default:"50" help:"Maximum files to list; 0 lists all."`
}

// Validate rejects negative --top values.
func (c FilesCmd) Validate() error {
	return validateTop(c.Top)
}

// Run executes `pan scan files`.
func (c FilesCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, err := deps.App.Snapshot(ctx, root.Repo)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, limitFiles(snap.Files, c.Top))
}

// SymbolsCmd is `pan scan symbols`.
type SymbolsCmd struct {
	Query string `name:"query" help:"Case-insensitive substring filter on symbol names."`
	Top   int    `name:"top" default:"50" help:"Maximum symbols to list; 0 lists all."`
}

// Validate rejects negative --top values.
func (c SymbolsCmd) Validate() error {
	return validateTop(c.Top)
}

// Run executes `pan scan symbols`.
func (c SymbolsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, symbols, err := deps.App.Symbols(ctx, root.Repo, c.Query, c.Top)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, symbols)
}

// RisksCmd is `pan scan risks`.
type RisksCmd struct {
	Limit        int         `name:"limit" default:"50" help:"Maximum files to list; 0 lists all."`
	Top          int         `name:"top" help:"Compatibility alias for --limit."`
	TopFiles     int         `name:"top-files" help:"Alias for --limit."`
	Language     string      `name:"language" help:"Restrict evidence to one supported language ID."`
	Intent       string      `name:"intent" short:"i" help:"Rerank admitted files for this audit intent."`
	IncludeClass []string    `name:"include-class" help:"Also admit a non-production file class (repeatable, or all)."`
	Detail       detailLevel `name:"detail" default:"compact" enum:"compact,evidence,paths" help:"Output detail: compact, evidence, or paths."`
}

// Validate rejects negative --top values.
func (c RisksCmd) Validate() error {
	if err := c.options().Validate(); err != nil {
		return err
	}
	return validateDetail(c.Detail)
}

// Run executes `pan scan risks`.
func (c RisksCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, risks, err := deps.App.RisksWithOptions(ctx, root.Repo, c.options())
	if err != nil {
		return err
	}
	if root.Format == formatJSON || !isTerminalWriter(deps.Out) {
		return emit(kctx, root, deps, snap, projectRiskReport(risks, c.Detail))
	}
	return writeRisks(deps.Out, snap, risks, c.Detail)
}

// SurfaceCmd is `pan scan surface`.
type SurfaceCmd struct {
	Limit    int    `name:"limit" default:"50" help:"Maximum files to list; 0 lists all."`
	Top      int    `name:"top" help:"Compatibility alias for --limit."`
	TopFiles int    `name:"top-files" help:"Alias for --limit."`
	Language string `name:"language" help:"Restrict evidence to one supported language ID."`
	Intent   string `name:"intent" short:"i" help:"Rerank admitted files for this audit intent."`
}

// Validate rejects negative --top values.
func (c SurfaceCmd) Validate() error { return c.options().Validate() }

// Run executes `pan scan surface`.
func (c SurfaceCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, surface, err := deps.App.SurfaceWithOptions(ctx, root.Repo, c.options())
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, surface)
}

// EffectsCmd is `pan scan effects`.
type EffectsCmd struct {
	Limit     int    `name:"limit" default:"50" help:"Maximum matching files to list; 0 lists all."`
	Top       int    `name:"top" help:"Compatibility alias for --limit; 0 uses --limit."`
	TopFiles  int    `name:"top-files" help:"Alias for --limit; 0 uses --limit."`
	Kind      string `name:"kind" help:"Restrict effects to one kind."`
	Language  string `name:"language" help:"Restrict effects to one supported language ID (go)."`
	Intent    string `name:"intent" short:"i" help:"Rerank admitted files for this audit intent."`
	PathsOnly bool   `name:"paths-only" help:"Emit only matching file paths, without the pan envelope."`
}

// Validate rejects invalid effects selectors.
func (c EffectsCmd) Validate() error {
	if err := validateTop(c.Limit); err != nil {
		return err
	}
	if err := validateTop(c.Top); err != nil {
		return err
	}
	if err := validateTop(c.TopFiles); err != nil {
		return err
	}
	limit := c.Limit
	if c.Top > 0 {
		limit = c.Top
	}
	if c.TopFiles > 0 {
		limit = c.TopFiles
	}
	return (scan.EffectsOptions{Limit: limit, Kind: c.Kind, Language: c.Language}).Validate()
}

// Run executes `pan scan effects`.
func (c EffectsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	limit := c.Limit
	if c.Top > 0 {
		limit = c.Top
	}
	if c.TopFiles > 0 {
		limit = c.TopFiles
	}
	snap, effects, err := deps.App.EffectsWithAuditOptions(ctx, root.Repo, app.AuditOptions{Limit: limit, Language: c.Language, Intent: c.Intent}, c.Kind)
	if err != nil {
		return err
	}
	if c.PathsOnly {
		return writeEffectPaths(deps.Out, root.Format, effects.EffectPaths())
	}
	return emit(kctx, root, deps, snap, effects)
}

func (c RisksCmd) options() app.AuditOptions {
	options := scanAuditOptions(c.Limit, c.Top, c.TopFiles, c.Language, c.Intent)
	options.IncludeClasses = c.IncludeClass
	return options
}

func (c SurfaceCmd) options() app.AuditOptions {
	return scanAuditOptions(c.Limit, c.Top, c.TopFiles, c.Language, c.Intent)
}

func scanAuditOptions(limit, top, topFiles int, language, intent string) app.AuditOptions {
	if top > 0 {
		limit = top
	}
	if topFiles > 0 {
		limit = topFiles
	}
	return app.AuditOptions{Limit: limit, Language: language, Intent: intent}
}

func writeEffectPaths(out io.Writer, format string, paths []string) error {
	if format == formatJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(paths)
	}
	for _, item := range paths {
		if _, err := fmt.Fprintln(out, item); err != nil {
			return err
		}
	}
	return nil
}

// HygieneCmd is `pan scan hygiene`.
type HygieneCmd struct{}

// Run executes `pan scan hygiene`.
func (HygieneCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, hygiene, err := deps.App.Hygiene(ctx, root.Repo)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, hygiene)
}

// ChangesCmd is `pan scan changes`.
type ChangesCmd struct {
	Days int `name:"days" default:"30" help:"History window in days; 0 uses the default window."`
	Top  int `name:"top" default:"20" help:"Maximum churn files to list; 0 lists all."`
}

// Validate rejects negative --days or --top values.
func (c ChangesCmd) Validate() error {
	if c.Days < 0 {
		return errors.New("--days must not be negative")
	}
	return validateTop(c.Top)
}

// Run executes `pan scan changes`.
func (c ChangesCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, changes, err := deps.App.Changes(ctx, root.Repo, c.Days, c.Top)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, changes)
}

// DoctorCmd is `pan scan doctor`: the deterministic local readiness check
// over configuration source, git availability, and one bounded analysis
// pass, optionally checking a loopback model catalog without credentials.
type DoctorCmd struct {
	Model     string `name:"model" help:"Check readiness for this model without requesting inference."`
	BaseURL   string `name:"base-url" default:"http://127.0.0.1:8000/v1" help:"Loopback model endpoint for optional readiness check."`
	APIKeyEnv string `name:"api-key-env" help:"Report whether this environment variable is present; never send it."`
	JSON      bool   `name:"json" help:"Emit the bare doctor report."`
}

// Run executes `pan scan doctor`.
func (c DoctorCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, report, err := deps.App.Doctor(ctx, root.Repo)
	if err != nil {
		return err
	}
	if c.Model != "" {
		health, err := scan.CheckProvider(ctx, c.Model, c.BaseURL, c.APIKeyEnv)
		if err != nil {
			return err
		}
		report.Provider = &health
	}
	if c.JSON {
		return json.NewEncoder(deps.Out).Encode(report)
	}
	if root.Format != formatJSON && isTerminalWriter(deps.Out) {
		return writeDoctor(deps.Out, snap, report)
	}
	return emit(kctx, root, deps, snap, report)
}

func limitFiles(files []analyze.File, top int) []analyze.File {
	if top > 0 && len(files) > top {
		return files[:top]
	}
	return files
}
