package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/scan"
)

// FlowGraphCmd is `pan flow graph`: the deterministic import dependency
// graph, optionally focused on one importing or imported package.
type FlowGraphCmd struct {
	Package string `name:"package" help:"Restrict graph to an importing or imported package."`
}

// Run executes `pan flow graph`.
func (c FlowGraphCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.FlowGraph(ctx, root.Repo, c.Package)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}

// FlowEndpointCmd is `pan flow endpoint`.
type FlowEndpointCmd struct {
	Route string `arg:"" help:"Route pattern, for example GET /health or /health."`
}

// Validate rejects a blank route selector.
func (c FlowEndpointCmd) Validate() error { return requireNonBlank(c.Route, "route") }

// Run executes `pan flow endpoint`.
func (c FlowEndpointCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.Endpoint(ctx, root.Repo, c.Route)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}

// OrphansCmd is `pan scan orphans`.
type OrphansCmd struct {
	Top int `name:"top" default:"50" help:"Maximum zero-reference candidates to list; 0 uses the bounded maximum."`
}

// Validate rejects negative --top values.
func (c OrphansCmd) Validate() error { return validateTop(c.Top) }

// Run executes `pan scan orphans`.
func (c OrphansCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.Orphans(ctx, root.Repo, c.Top)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}

// InventoryCmd is `pan scan inventory`.
type InventoryCmd struct {
	Boundary string `name:"boundary" required:"" help:"Boundary name, such as Postgres, filesystem, HTTP, or subprocess."`
	Top      int    `name:"top" default:"50" help:"Maximum owners to list; 0 uses the bounded maximum."`
}

// Validate rejects a negative --top value.
func (c InventoryCmd) Validate() error {
	if c.Top < 0 {
		return errors.New("--top must not be negative")
	}
	return nil
}

// Run executes `pan scan inventory`.
func (c InventoryCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.Inventory(ctx, root.Repo, c.Boundary, c.Top)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}

// ReviewBriefCmd is `pan review brief`.
type ReviewBriefCmd struct {
	Limit              int    `name:"limit" default:"25" help:"Maximum files to emit; 0 lists all bounded results."`
	Top                int    `name:"top" help:"Compatibility alias for --limit."`
	TopFiles           int    `name:"top-files" help:"Alias for --limit."`
	Language           string `name:"language" help:"Restrict evidence to one supported language ID."`
	Intent             string `name:"intent" short:"i" help:"Rerank admitted files for this audit intent."`
	HistoryWindow      int    `name:"history-window" help:"Include bounded Git history from this many commits; 0 omits it."`
	RefactorSignatures bool   `name:"refactor-signatures" help:"Include exact duplicate Go function-body signatures."`
}

// Validate rejects negative --top values.
func (c ReviewBriefCmd) Validate() error { return c.options().Validate() }

// Run executes `pan review brief`.
func (c ReviewBriefCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.ReviewBriefWithOptions(ctx, root.Repo, c.options())
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}

// ReviewRisksCmd is `pan review risks`.
type ReviewRisksCmd struct {
	Limit        int         `name:"limit" default:"50" help:"Maximum risk files; 0 lists all bounded results."`
	Top          int         `name:"top" help:"Compatibility alias for --limit."`
	TopFiles     int         `name:"top-files" help:"Alias for --limit."`
	Language     string      `name:"language" help:"Restrict evidence to one supported language ID."`
	Intent       string      `name:"intent" short:"i" help:"Rerank admitted files for this audit intent."`
	IncludeClass []string    `name:"include-class" help:"Also admit a non-production file class (repeatable, or all)."`
	Detail       detailLevel `name:"detail" default:"compact" enum:"compact,evidence,paths" help:"Output detail: compact, evidence, or paths."`
}

// Validate rejects negative --top values.
func (c ReviewRisksCmd) Validate() error {
	if err := c.options().Validate(); err != nil {
		return err
	}
	return validateDetail(c.Detail)
}

// Run executes `pan review risks`.
func (c ReviewRisksCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.RisksWithOptions(ctx, root.Repo, c.options())
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, projectRiskReport(result, c.Detail))
}

// ReviewEffectsCmd is `pan review effects`.
type ReviewEffectsCmd struct {
	Limit    int    `name:"limit" default:"50" help:"Maximum effect files; 0 lists all bounded results."`
	Top      int    `name:"top" help:"Compatibility alias for --limit."`
	TopFiles int    `name:"top-files" help:"Alias for --limit."`
	Kind     string `name:"kind" help:"Optional effect kind filter."`
	Language string `name:"language" help:"Restrict evidence to one supported language ID."`
	Intent   string `name:"intent" short:"i" help:"Rerank admitted files for this audit intent."`
}

// Validate rejects a negative --top or an unknown effect kind.
func (c ReviewEffectsCmd) Validate() error {
	if err := c.options().Validate(); err != nil {
		return err
	}
	if c.Kind != "" && !slices.Contains(scan.EffectKinds(), c.Kind) {
		return fmt.Errorf("unknown effect kind %q", c.Kind)
	}
	return nil
}

// Run executes `pan review effects`, narrowing to one effect kind when given.
func (c ReviewEffectsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.EffectsWithAuditOptions(ctx, root.Repo, c.options(), c.Kind)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}

func (c ReviewBriefCmd) options() app.AuditOptions {
	return auditOptions(auditSelector{c.Limit, c.Top, c.TopFiles, c.Language, c.Intent}, c.HistoryWindow, c.RefactorSignatures)
}

func (c ReviewRisksCmd) options() app.AuditOptions {
	options := auditOptions(auditSelector{c.Limit, c.Top, c.TopFiles, c.Language, c.Intent}, 0, false)
	options.IncludeClasses = c.IncludeClass
	return options
}

func (c ReviewEffectsCmd) options() app.AuditOptions {
	return auditOptions(auditSelector{c.Limit, c.Top, c.TopFiles, c.Language, c.Intent}, 0, false)
}

type auditSelector struct {
	limit, top, topFiles int
	language, intent     string
}

func auditOptions(selector auditSelector, historyWindow int, refactorSignatures bool) app.AuditOptions {
	if selector.top > 0 {
		selector.limit = selector.top
	}
	if selector.topFiles > 0 {
		selector.limit = selector.topFiles
	}
	return app.AuditOptions{Limit: selector.limit, Language: selector.language, Intent: selector.intent, HistoryWindow: historyWindow, RefactorSignatures: refactorSignatures}
}
