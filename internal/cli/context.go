package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/cache"
	"github.com/dotcommander/pan/internal/retrieval"
	"github.com/dotcommander/pan/internal/retrievalstats"
)

// ContextCmd groups task-oriented code retrieval and repository context.
type ContextCmd struct {
	Brief     ContextBriefCmd     `cmd:"" help:"Task-oriented repository summary."`
	Map       ContextMapCmd       `cmd:"" help:"Ranked, token-budgeted repository map."`
	Task      ContextTaskCmd      `cmd:"" help:"Goal-oriented context packet."`
	Find      ContextFindCmd      `cmd:"" help:"Symbol search."`
	Symbol    ContextSymbolCmd    `cmd:"" help:"Symbol detail with source context."`
	Explain   ContextExplainCmd   `cmd:"" help:"Ranking explanation for a file."`
	Impact    ContextImpactCmd    `cmd:"" help:"Evidence-backed blast radius for a file."`
	Endpoint  ContextEndpointCmd  `cmd:"" help:"Route-to-handler evidence."`
	Lsp       ContextLspCmd       `cmd:"" help:"Bounded language-server queries."`
	Preflight ContextPreflightCmd `cmd:"" help:"Resolve applicable repository guidance and purpose."`
	Init      ContextInitCmd      `cmd:"" help:"Create repository analysis scaffolding and an optional cache hook."`
	Eval      ContextEvalCmd      `cmd:"" help:"Evaluate retrieval cases."`
	Savings   ContextSavingsCmd   `cmd:"" help:"Summarize recorded task-packet token savings."`
}

// ContextEvalCmd is `pan context eval`.
type ContextEvalCmd struct {
	Cases      string `name:"cases" type:"path" required:""`
	Policy     string `name:"policy" default:"structural-lexical/v1"`
	Efficiency bool   `name:"efficiency" help:"Add the modeled grep+read baseline and recall-at-budget curves."`
}

// Run evaluates the configured retrieval cases.
func (c ContextEvalCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	cases, err := retrieval.LoadEvalCases(c.Cases)
	if err != nil {
		return err
	}
	snap, err := deps.App.Snapshot(ctx, root.Repo)
	if err != nil {
		return err
	}
	report, err := retrieval.Evaluate(snap, cases, c.Policy, retrieval.EvalOptions{Efficiency: c.Efficiency})
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, report)
}

// ContextSavingsCmd is `pan context savings`.
type ContextSavingsCmd struct{}

// Run summarizes the recorded retrieval savings ledger.
func (c ContextSavingsCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	base, err := cache.DefaultDir()
	if err != nil {
		return err
	}
	summary, err := retrievalstats.Summarize(retrievalstats.Path(base), time.Now())
	if err != nil {
		return err
	}
	if root.Format == formatJSON {
		encoder := json.NewEncoder(deps.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(summary)
	}
	_, err = fmt.Fprint(deps.Out, retrievalstats.Render(summary))
	return err
}

// ContextInitCmd is `pan context init`, compatible with Pan's local
// scaffold while keeping Pan configuration ownership separate.
type ContextInitCmd struct {
	Directory string `arg:"" optional:"" type:"path" help:"Repository directory; when present, overrides --repo."`
	Force     bool   `name:"force" help:"Replace Pan-owned scaffold files and hooks."`
	NoHook    bool   `name:"no-hook" help:"Do not create a post-commit cache hook."`
	NoConfig  bool   `name:"no-config" help:"Do not create the analysis config scaffold."`
}

// Run executes `pan context init`.
func (c ContextInitCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	directory := root.Repo
	if c.Directory != "" {
		directory = c.Directory
	}
	result, err := runContextInit(directory, c.Force, c.NoHook, c.NoConfig)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, result.Directory, result)
}

// briefOptions is shared by `context brief` and the top-level `brief`
// compatibility alias so both accept identical arguments and validation.
type briefOptions struct {
	Intent string      `arg:"" optional:"" help:"Task intent orienting the summary."`
	Budget int         `name:"budget" default:"4096" help:"Approximate output budget in bytes."`
	Detail detailLevel `name:"detail" default:"compact" enum:"compact,evidence,paths" help:"Output detail: compact, evidence, or paths."`
}

func (o briefOptions) Validate() error {
	if o.Budget < 1 {
		return errors.New("--budget must be positive")
	}
	return nil
}

// ContextBriefCmd is `pan context brief`.
type ContextBriefCmd struct{ briefOptions }

// BriefAliasCmd is the top-level `pan brief` compatibility alias.
type BriefAliasCmd struct{ briefOptions }

// Run executes `pan context brief`.
func (ContextBriefCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	return runBrief(kctx, root, deps, ctx, root.Context.Brief.briefOptions)
}

// Run executes the top-level `pan brief` compatibility alias.
func (BriefAliasCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	return runBrief(kctx, root, deps, ctx, root.Brief.briefOptions)
}

const (
	briefFilesKey        = "files"
	briefInstructionsKey = "instructions"
	briefNextCommandsKey = "next_commands"
	briefStateKey        = "state"
	briefSymbolsKey      = "symbols"
	briefTopFilesKey     = "top_files"
	briefVerifyKey       = "verify"
	briefMapKey          = "map"
	diagnosticWarning    = "warning"
)

type briefText struct {
	brief    app.BriefResult
	state    briefState
	top      []app.BriefEntry
	commands []app.BriefNextCommand
	detail   detailLevel
}

type briefPresentation struct {
	instructions []string
	top          []app.BriefEntry
	commands     []app.BriefNextCommand
	omitted      []string
	truncations  []string
	used         int
	truncated    bool
}

func runBrief(kctx *kong.Context, root *Root, deps Deps, ctx context.Context, opts briefOptions) error {
	opts.Detail = normalizeDetail(opts.Detail)
	brief, err := deps.App.Brief(ctx, root.Repo, opts.Intent, opts.Budget)
	if err != nil {
		return err
	}
	presentation := projectBrief(brief, opts.Detail)
	presentation.trimToBudget(opts.Budget)
	state := briefRepositoryState(ctx, brief.Snapshot.Root)
	result := briefJSONResult(brief, state, opts, presentation)
	if root.Format == formatJSON {
		return emit(kctx, root, deps, brief.Snapshot, result)
	}
	return writeBrief(deps, briefText{brief: brief, state: state, top: presentation.top, commands: presentation.commands, detail: opts.Detail})
}

func projectBrief(brief app.BriefResult, detail detailLevel) briefPresentation {
	presentation := briefPresentation{
		instructions: slices.Clone(brief.Snapshot.Instructions),
		top:          slices.Clone(brief.Entries),
		commands:     slices.Clone(brief.NextCommands),
	}
	if presentation.commands == nil {
		presentation.commands = []app.BriefNextCommand{}
	}
	switch detail {
	case detailCompact:
		presentation.omitted = []string{briefInstructionsKey, "map", "top_files[].components", "top_files[].families"}
		presentation.instructions = nil
		for i := range presentation.top {
			presentation.top[i].Components = nil
			presentation.top[i].Families = nil
		}
	case detailEvidence:
		// Evidence retains all fields.
	case detailPaths:
		presentation.omitted = []string{briefTopFilesKey, briefFilesKey, briefSymbolsKey, briefVerifyKey, briefStateKey, briefInstructionsKey, briefMapKey, briefNextCommandsKey}
		presentation.instructions = nil
		for i := range presentation.top {
			presentation.top[i] = app.BriefEntry{Path: presentation.top[i].Path}
		}
	}
	return presentation
}

func (p *briefPresentation) trimToBudget(budget int) {
	p.used = briefVariableBytes(p.instructions, p.top, p.commands)
	for i := len(p.top) - 1; p.used > budget && i >= 0; i-- {
		if len(p.top[i].Families) == 0 {
			continue
		}
		p.top[i].Families = nil
		p.markTruncated("symbol_families")
		p.used = briefVariableBytes(p.instructions, p.top, p.commands)
	}
	for p.used > budget && len(p.top) > 0 {
		p.top = p.top[:len(p.top)-1]
		p.markTruncated("ranked_files")
		p.used = briefVariableBytes(p.instructions, p.top, p.commands)
	}
	for p.used > budget && len(p.commands) > 0 {
		p.commands = p.commands[:len(p.commands)-1]
		p.markTruncated("suggested_commands")
		p.used = briefVariableBytes(p.instructions, p.top, p.commands)
	}
	for p.used > budget && len(p.instructions) > 0 {
		p.instructions = p.instructions[:len(p.instructions)-1]
		p.markTruncated("instructions")
		p.used = briefVariableBytes(p.instructions, p.top, p.commands)
	}
	if p.truncations == nil {
		p.truncations = []string{}
	}
}

func (p *briefPresentation) markTruncated(kind string) {
	p.truncated = true
	if !slices.Contains(p.truncations, kind) {
		p.truncations = append(p.truncations, kind)
	}
}

func briefJSONResult(brief app.BriefResult, state briefState, opts briefOptions, presentation briefPresentation) map[string]any {
	result := map[string]any{
		"detail":             opts.Detail,
		"omitted_fields":     sortedStrings(presentation.omitted),
		"intent":             opts.Intent,
		briefInstructionsKey: presentation.instructions,
		briefFilesKey:        len(brief.Snapshot.Files),
		briefSymbolsKey:      len(brief.Snapshot.Symbols),
		briefTopFilesKey:     presentation.top,
		"budget":             opts.Budget,
		briefVerifyKey:       briefVerifyCommands(brief.Snapshot.Root),
		briefStateKey:        state,
		"analysis":           makeBriefAnalysis(brief),
		briefNextCommandsKey: presentation.commands,
		"budget_info":        map[string]any{"unit": "bytes", "requested": opts.Budget, "scope": "variable_content", "used": presentation.used, "truncated": presentation.truncated, "truncations": slices.Clone(presentation.truncations)},
		"truncated":          presentation.truncated,
	}
	switch opts.Detail {
	case detailPaths:
		result["paths"] = briefPaths(presentation.top)
		for _, field := range []string{briefTopFilesKey, briefFilesKey, briefSymbolsKey, briefVerifyKey, briefStateKey, briefNextCommandsKey} {
			delete(result, field)
		}
		delete(result, briefInstructionsKey)
	case detailEvidence:
		result[briefMapKey] = brief.Map
	case detailCompact:
		delete(result, briefInstructionsKey)
	}
	return result
}

func briefPaths(top []app.BriefEntry) []string {
	paths := make([]string, 0, len(top))
	for _, entry := range top {
		paths = append(paths, entry.Path)
	}
	return paths
}

// briefAnalysis is the stable, answer-ready analysis shape. Keep bounded
// status arrays present even when empty so an agent can distinguish complete
// evidence from an omitted field or a serialization null.
type briefAnalysisJSON struct {
	AnalyzedFiles int             `json:"analyzed_files"`
	TotalFiles    *int            `json:"total_files"`
	Complete      bool            `json:"complete"`
	Limits        []string        `json:"limits"`
	Skipped       []string        `json:"skipped"`
	SkippedCount  int             `json:"skipped_count"`
	Warnings      []string        `json:"warnings"`
	Confidence    string          `json:"confidence"`
	Classes       map[string]int  `json:"classes,omitempty"`
	Bounds        app.BriefBounds `json:"bounds"`
}

func makeBriefAnalysis(result app.BriefResult) briefAnalysisJSON {
	limits := slices.Clone(result.Coverage.Limits)
	if limits == nil {
		limits = []string{}
	}
	skipped := slices.Clone(result.Coverage.Skipped)
	if skipped == nil {
		skipped = []string{}
	}
	warnings := make([]string, 0)
	for _, diagnostic := range result.Snapshot.Diagnostics {
		if diagnostic.Level == diagnosticWarning {
			warnings = append(warnings, diagnostic.Message)
		}
	}
	confidence := "high"
	if !result.Coverage.Complete || len(limits) > 0 || len(warnings) > 0 {
		confidence = "limited"
	}
	return briefAnalysisJSON{
		AnalyzedFiles: result.Coverage.AnalyzedFiles,
		TotalFiles:    result.Coverage.TotalFiles,
		Complete:      result.Coverage.Complete,
		Limits:        limits,
		Skipped:       skipped,
		SkippedCount:  result.Coverage.SkippedCount,
		Warnings:      warnings,
		Confidence:    confidence,
		Classes:       result.Coverage.Classes,
		Bounds:        result.Coverage.Bounds,
	}
}

// writeBrief renders the source-compatible orientation sections that matter
// before an agent edits code: declared verification commands, repository state,
// and a bounded rendered symbol map.
func writeBrief(deps Deps, text briefText) error {
	if text.detail == detailPaths {
		return writeBriefPaths(deps, text.top)
	}
	if err := writeBriefAnalysis(deps, text.brief); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(deps.Out); err != nil {
		return err
	}
	if err := writeBriefVerification(deps, briefVerifyCommands(text.state.Repository)); err != nil {
		return err
	}
	if err := writeBriefState(deps, text.state); err != nil {
		return err
	}
	if err := writeBriefRankedFiles(deps, text.top); err != nil {
		return err
	}
	if err := writeBriefFamilies(deps, text.top, text.detail); err != nil {
		return err
	}
	if err := writeBriefCommands(deps, text.commands); err != nil {
		return err
	}
	if text.detail != detailEvidence {
		return nil
	}
	_, err := fmt.Fprintf(deps.Out, "\n## Map\n%s", text.brief.Map.Text)
	return err
}

func writeBriefPaths(deps Deps, top []app.BriefEntry) error {
	for _, entry := range top {
		if _, err := fmt.Fprintln(deps.Out, entry.Path); err != nil {
			return err
		}
	}
	return nil
}

func writeBriefAnalysis(deps Deps, brief app.BriefResult) error {
	totalFiles := "unknown"
	if brief.Coverage.TotalFiles != nil {
		totalFiles = strconv.Itoa(*brief.Coverage.TotalFiles)
	}
	if _, err := fmt.Fprintf(deps.Out, "## Analysis\n  complete: %t   files: %d/%s\n", brief.Coverage.Complete, brief.Coverage.AnalyzedFiles, totalFiles); err != nil {
		return err
	}
	if len(brief.Coverage.Limits) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(deps.Out, "  limits: %s\n", strings.Join(brief.Coverage.Limits, ", "))
	return err
}

func writeBriefRankedFiles(deps Deps, top []app.BriefEntry) error {
	if len(top) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(deps.Out, "\n## Ranked files"); err != nil {
		return err
	}
	for _, entry := range top {
		if err := writeBriefRankedFile(deps, entry); err != nil {
			return err
		}
	}
	return nil
}

func writeBriefRankedFile(deps Deps, entry app.BriefEntry) error {
	if _, err := fmt.Fprintf(deps.Out, "  %s  score=%d class=%s", entry.Path, entry.Score, entry.Class); err != nil {
		return err
	}
	if entry.Tag != "" {
		if _, err := fmt.Fprintf(deps.Out, " tag=%s", entry.Tag); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(deps.Out)
	return err
}

func writeBriefFamilies(deps Deps, top []app.BriefEntry, detail detailLevel) error {
	if detail == detailEvidence {
		if _, err := fmt.Fprintln(deps.Out, "\n## Symbol families"); err != nil {
			return err
		}
	}
	for _, entry := range top {
		if len(entry.Families) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(deps.Out, "  %s: %v\n", entry.Path, entry.Families); err != nil {
			return err
		}
	}
	return nil
}

func writeBriefCommands(deps Deps, commands []app.BriefNextCommand) error {
	if _, err := fmt.Fprintln(deps.Out, "\n## Suggested commands"); err != nil {
		return err
	}
	for _, command := range commands {
		if _, err := fmt.Fprintf(deps.Out, "  pan %s  # %s\n", strings.Join(command.Args, " "), command.Reason); err != nil {
			return err
		}
	}
	return nil
}

func briefVariableBytes(instructions []string, top []app.BriefEntry, commands []app.BriefNextCommand) int {
	used := 0
	for _, value := range instructions {
		encoded, _ := json.Marshal(value)
		used += len(encoded)
	}
	for _, value := range top {
		encoded, _ := json.Marshal(value)
		used += len(encoded)
	}
	for _, value := range commands {
		encoded, _ := json.Marshal(value)
		used += len(encoded)
	}
	return used
}

func writeBriefVerification(deps Deps, verification briefVerification) error {
	if _, err := fmt.Fprintf(deps.Out, "## Verify\n  build: %s\n  test:  %s\n", verification.Build, verification.Test); err != nil {
		return err
	}
	if verification.Vet == "" {
		return nil
	}
	_, err := fmt.Fprintf(deps.Out, "  vet:   %s\n", verification.Vet)
	return err
}

func writeBriefState(deps Deps, state briefState) error {
	if _, err := fmt.Fprintf(deps.Out, "\n## State\n  branch: %s   dirty: %d file(s)\n", state.Branch, len(state.Dirty)); err != nil {
		return err
	}
	for i, line := range state.Dirty {
		if i == 8 {
			if _, err := fmt.Fprintf(deps.Out, "    +%d more\n", len(state.Dirty)-i); err != nil {
				return err
			}
			break
		}
		if _, err := fmt.Fprintf(deps.Out, "    %s\n", line); err != nil {
			return err
		}
	}
	if len(state.Recent) > 0 {
		if _, err := fmt.Fprintln(deps.Out, "  recent:"); err != nil {
			return err
		}
		for _, subject := range state.Recent {
			if _, err := fmt.Fprintf(deps.Out, "    %s\n", subject); err != nil {
				return err
			}
		}
	}
	return nil
}

type briefVerification struct {
	Build string `json:"build"`
	Test  string `json:"test"`
	Vet   string `json:"vet,omitempty"`
}

type briefState struct {
	Repository string   `json:"-"`
	Branch     string   `json:"branch"`
	Dirty      []string `json:"dirty,omitempty"`
	Recent     []string `json:"recent,omitempty"`
}

func briefVerifyCommands(root string) briefVerification {
	if briefFileExists(root, "go.mod") {
		return briefVerification{Build: "go build ./...", Test: "go test ./...", Vet: "go vet ./..."}
	}
	if briefFileExists(root, "package.json") {
		return briefVerification{Build: "npm run build", Test: "npm test"}
	}
	return briefVerification{Build: "(unknown)", Test: "(unknown)"}
}

func briefRepositoryState(ctx context.Context, root string) briefState {
	state := briefState{Repository: root, Branch: briefGitOutput(ctx, "-C", root, "branch", "--show-current")}
	if state.Branch == "" {
		state.Branch = "(none)"
	}
	state.Dirty = briefGitLines(ctx, "-C", root, "status", "--short")
	state.Recent = briefGitLines(ctx, "-C", root, "log", "-3", "--format=%s")
	return state
}

func briefFileExists(root, name string) bool {
	info, err := os.Stat(filepath.Join(root, name))
	return err == nil && !info.IsDir()
}

func briefGitOutput(ctx context.Context, args ...string) string {
	// #nosec G204 -- the executable is fixed and every argument is generated locally.
	output, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func briefGitLines(ctx context.Context, args ...string) []string {
	output := briefGitOutput(ctx, args...)
	if output == "" {
		return nil
	}
	return strings.Split(output, "\n")
}
