package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/codemap"
	"github.com/dotcommander/pan/internal/render"
	"github.com/dotcommander/pan/internal/retrieval"
)

// requireNonBlank rejects blank required positional selectors before any
// analysis runs.
func requireNonBlank(value, name string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be blank", name)
	}
	return nil
}

// requireNonNegative rejects negative numeric flags; zero keeps each
// command's documented zero semantics.
func requireNonNegative(value int, flag string) error {
	if value < 0 {
		return fmt.Errorf("%s must not be negative", flag)
	}
	return nil
}

// ContextMapCmd is `pan context map`: the ranked, token-budgeted
// repository map rendered in enriched or compact mode.
type ContextMapCmd struct {
	Mode              string   `name:"mode" default:"enriched" enum:"enriched,compact" help:"Legacy map rendering mode (enriched or compact)."`
	Format            string   `name:"map-format" short:"f" default:"enriched" enum:"enriched,compact,verbose,detail,lines,xml" help:"Map rendering format."`
	Tokens            int      `name:"tokens" short:"t" default:"2048" help:"Approximate token budget; must be greater than zero."`
	Intent            string   `name:"intent" help:"Task intent orienting file ranking."`
	Consumed          []string `name:"consumed" sep:"," help:"Comma-separated repo-relative paths already in context."`
	JSON              bool     `name:"json" help:"Emit the source-compatible rendered-lines JSON envelope."`
	JSONStructured    bool     `name:"json-structured" help:"Emit the map in Pan's structured JSON envelope."`
	Calls             bool     `name:"calls" help:"Include call-edge evidence when available."`
	CallsThreshold    int      `name:"calls-threshold" default:"2" help:"Minimum call evidence threshold."`
	CallsLimit        int      `name:"calls-limit" default:"10" help:"Maximum call evidence rows."`
	CallsIncludeTests bool     `name:"calls-include-tests" help:"Include test call evidence."`
	SymbolRefs        bool     `name:"symbol-refs" help:"Include symbol reference evidence."`
	ExplainScores     bool     `name:"explain" help:"Include score explanations."`
	IncludeTests      bool     `name:"include-tests" help:"Rank test files at full weight instead of the default demotion."`
}

// Validate rejects a non-positive token budget.
func (c ContextMapCmd) Validate() error {
	if c.Tokens <= 0 {
		return errors.New("--tokens must be greater than zero")
	}
	if err := requireNonNegative(c.CallsThreshold, "--calls-threshold"); err != nil {
		return err
	}
	if err := requireNonNegative(c.CallsLimit, "--calls-limit"); err != nil {
		return err
	}
	if c.JSON && c.JSONStructured {
		return errors.New("--json and --json-structured cannot be used together")
	}
	return nil
}

// Run executes `pan context map`.
func (c ContextMapCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	mode := c.Format
	if c.Mode != codemap.ModeEnriched {
		mode = c.Mode
	}
	if c.JSON {
		mode = codemap.ModeVerbose
	}
	opts := codemap.Options{
		Mode: mode, Tokens: c.Tokens, Intent: c.Intent, Consumed: c.Consumed,
		Calls: c.Calls, CallsThreshold: c.CallsThreshold, CallsLimit: c.CallsLimit,
		CallsIncludeTests: c.CallsIncludeTests, SymbolRefs: c.SymbolRefs,
		ExplainScores: c.ExplainScores, IncludeTests: c.IncludeTests,
	}
	if c.JSONStructured {
		output, err := deps.App.MapStructured(ctx, root.Repo, opts)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(deps.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	snap, result, err := deps.App.Map(ctx, root.Repo, opts)
	if err != nil {
		return err
	}
	if !c.JSON && !c.JSONStructured && root.Format != formatJSON {
		_, err := fmt.Fprint(deps.Out, result.Text)
		return err
	}
	if c.JSON {
		output := mapJSONOutput{
			SchemaVersion: 2,
			Totals:        mapJSONTotals{Files: result.TotalFiles, Symbols: result.TotalSymbols},
			Selection: mapJSONSelection{
				TotalFiles: result.TotalFiles, TotalSymbols: result.TotalSymbols,
				SelectedFiles: result.ShownFiles, SelectedSymbols: result.ShownSymbols,
				OmittedFiles: result.OmittedFiles, OmittedSymbols: result.TotalSymbols - result.ShownSymbols,
			},
			Lines: strings.Split(strings.TrimSuffix(result.Text, "\n"), "\n"),
		}
		if output.Selection.OmittedFiles > 0 || output.Selection.OmittedSymbols > 0 {
			output.Selection.OmittedReason = "complete-output token budget"
		}
		encoder := json.NewEncoder(deps.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	outputRoot := *root
	if c.JSON || c.JSONStructured {
		outputRoot.Format = formatJSON
	}
	return emit(kctx, &outputRoot, deps, snap, result)
}

type mapJSONOutput struct {
	SchemaVersion int              `json:"schema_version"`
	Totals        mapJSONTotals    `json:"totals"`
	Selection     mapJSONSelection `json:"selection"`
	Lines         []string         `json:"lines"`
}

type mapJSONTotals struct {
	Files   int `json:"files"`
	Symbols int `json:"symbols"`
}

type mapJSONSelection struct {
	TotalFiles      int    `json:"total_files"`
	TotalSymbols    int    `json:"total_symbols"`
	SelectedFiles   int    `json:"selected_files"`
	SelectedSymbols int    `json:"selected_symbols"`
	OmittedFiles    int    `json:"omitted_files"`
	OmittedSymbols  int    `json:"omitted_symbols"`
	OmittedReason   string `json:"omitted_reason,omitempty"`
}

// ContextTaskCmd is `pan context task`: the bounded goal-oriented context
// packet with target selection, evidence, and source excerpts.
type ContextTaskCmd struct {
	Goal     string   `arg:"" help:"Implementation goal orienting the packet."`
	Tokens   int      `name:"tokens" default:"4096" help:"Approximate token budget; 0 uses the default."`
	Consumed []string `name:"consumed" sep:"," help:"Comma-separated repo-relative paths already in context."`
}

// Validate rejects a blank goal or negative token budget.
func (c ContextTaskCmd) Validate() error {
	if err := requireNonBlank(c.Goal, "task goal"); err != nil {
		return err
	}
	return requireNonNegative(c.Tokens, "--tokens")
}

// Run executes `pan context task`.
func (c ContextTaskCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, report, err := deps.App.Task(ctx, root.Repo, c.Goal, retrieval.TaskOptions{
		Tokens:   c.Tokens,
		Consumed: c.Consumed,
	})
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, report)
}

// ContextFindCmd is `pan context find`: ranked symbol search with optional
// kind and file filters and an exact-prefix "symbol:" handle grammar.
type ContextFindCmd struct {
	Query string `arg:"" help:"Symbol query; supports kind:/file: qualifiers and symbol: handles."`
	Kind  string `name:"kind" help:"Filter by symbol kind (function, struct, interface, ...)."`
	File  string `name:"file" help:"Filter to files containing this substring."`
	Top   int    `name:"top" default:"20" help:"Maximum matches to list; 0 lists all."`
	Limit *int   `name:"limit" help:"Pan-compatible maximum matches; 0 lists all."`
}

// Validate rejects a blank query or negative --top.
func (c ContextFindCmd) Validate() error {
	if err := requireNonBlank(c.Query, "symbol query"); err != nil {
		return err
	}
	if err := requireNonNegative(c.Top, "--top"); err != nil {
		return err
	}
	if c.Limit != nil {
		return requireNonNegative(*c.Limit, "--limit")
	}
	return nil
}

// Run executes `pan context find`.
func (c ContextFindCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, matches, err := deps.App.Find(ctx, root.Repo, c.Query, c.Kind, c.File)
	if err != nil {
		return err
	}
	limit := c.Top
	if c.Limit != nil {
		limit = *c.Limit
	}
	matches = limitMatches(matches, limit)
	// Pan's find --format json is a raw array, including [] when there
	// are no matches. Root's global format flag provides that compatibility
	// spelling for this command.
	if root.Format == formatJSON {
		if matches == nil {
			matches = []retrieval.SymbolMatch{}
		}
		encoder := json.NewEncoder(deps.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(matches)
	}
	return emit(kctx, root, deps, snap, matches)
}

// ContextSymbolCmd is `pan context symbol`: the best match for one query
// with impact, lexical callers, and a bounded source excerpt.
type ContextSymbolCmd struct {
	Query             string `arg:"" help:"Symbol query; supports kind:/file: qualifiers and symbol: handles."`
	Kind              string `name:"kind" help:"Filter by symbol kind (function, struct, interface, ...)."`
	File              string `name:"file" help:"Filter to files containing this substring."`
	Lines             int    `name:"lines" default:"200" help:"Maximum source excerpt lines; 0 uses the default."`
	MaxSourceLines    *int   `name:"max-source-lines" help:"Compatibility alias for --lines; must be greater than zero."`
	MaxOutputLines    int    `name:"max-output-lines" default:"400" help:"Maximum text output lines; 0 is unlimited."`
	MaxOutputBytes    int    `name:"max-output-bytes" default:"65536" help:"Maximum text output bytes; 0 is unlimited."`
	Calls             bool   `name:"calls" help:"Include lexical callers of the matched symbol."`
	CallsIncludeTests bool   `name:"calls-include-tests" help:"Include _test.go callers when --calls is set."`
	CallsLimit        int    `name:"calls-limit" default:"10" help:"Maximum callers when --calls is set; 0 is unlimited."`
}

// Validate rejects a blank query or negative --lines.
func (c ContextSymbolCmd) Validate() error {
	if err := requireNonBlank(c.Query, "symbol query"); err != nil {
		return err
	}
	if err := requireNonNegative(c.Lines, "--lines"); err != nil {
		return err
	}
	if c.MaxSourceLines != nil && *c.MaxSourceLines < 1 {
		return errors.New("--max-source-lines must be greater than zero")
	}
	for _, option := range []struct {
		name  string
		value int
	}{
		{"--max-output-lines", c.MaxOutputLines},
		{"--max-output-bytes", c.MaxOutputBytes},
		{"--calls-limit", c.CallsLimit},
	} {
		if err := requireNonNegative(option.value, option.name); err != nil {
			return err
		}
	}
	return nil
}

// Run executes `pan context symbol`.
func (c ContextSymbolCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	maxSourceLines := c.Lines
	if c.MaxSourceLines != nil {
		maxSourceLines = *c.MaxSourceLines
	}
	snap, result, err := deps.App.SymbolContext(ctx, root.Repo, c.Query, retrieval.SymbolOptions{
		Kind:              c.Kind,
		File:              c.File,
		MaxSourceLines:    maxSourceLines,
		Calls:             c.Calls,
		CallsIncludeTests: c.CallsIncludeTests,
		CallsLimit:        c.CallsLimit,
	})
	if err != nil {
		return err
	}
	return emitSymbol(kctx, root, deps, symbolOutput{
		snapshot: snap,
		result:   result,
		maxLines: c.MaxOutputLines,
		maxBytes: c.MaxOutputBytes,
	})
}

type symbolOutput struct {
	snapshot analyze.Snapshot
	result   retrieval.SymbolResult
	maxLines int
	maxBytes int
}

// emitSymbol preserves JSON's complete structured result. Text output follows
// Pan's source contract: its rendered envelope is capped after formatting.
func emitSymbol(kctx *kong.Context, root *Root, deps Deps, output symbolOutput) error {
	if root.Format == formatJSON {
		return emit(kctx, root, deps, output.snapshot, output.result)
	}
	envelope := render.NewEnvelope(commandPath(kctx), output.snapshot.Root, output.snapshot.Status, output.result, output.snapshot.Diagnostics)
	var raw bytes.Buffer
	if err := render.Write(&raw, "text", envelope); err != nil {
		return err
	}
	_, err := deps.Out.Write([]byte(formatBoundedText(raw.String(), output.maxLines, output.maxBytes).Text))
	return err
}

// ContextExplainCmd is `pan context explain`: why one file ranked and
// rendered the way it did, with score components and budget placement.
type ContextExplainCmd struct {
	File   string `arg:"" help:"Repository-relative file to explain."`
	Tokens int    `name:"tokens" default:"4096" help:"Token budget used for detail placement; 0 is unlimited."`
}

// Validate rejects a blank file or negative token budget.
func (c ContextExplainCmd) Validate() error {
	if err := requireNonBlank(c.File, "file"); err != nil {
		return err
	}
	return requireNonNegative(c.Tokens, "--tokens")
}

// Run executes `pan context explain`.
func (c ContextExplainCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.Explain(ctx, root.Repo, c.File, c.Tokens)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}

// ContextEndpointCmd is `pan context endpoint`: the vertical slice for one
// HTTP route, including handler resolution, lexical callees, tests, and
// the handler file's blast radius.
type ContextEndpointCmd struct {
	Route          string `arg:"" optional:"" help:"Route pattern, for example \"GET /health\" or \"/health\"; omit to list routes."`
	MaxOutputLines int    `name:"max-output-lines" default:"400" help:"Maximum route-detail text output lines; 0 is unlimited."`
}

// Validate permits an omitted route for route listing while still rejecting a
// supplied selector made entirely of whitespace.
func (c ContextEndpointCmd) Validate() error {
	if err := requireNonNegative(c.MaxOutputLines, "--max-output-lines"); err != nil {
		return err
	}
	if c.Route == "" {
		return nil
	}
	return requireNonBlank(c.Route, "route")
}

// Run executes `pan context endpoint`.
func (c ContextEndpointCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	if c.Route == "" {
		snap, routes, err := deps.App.Routes(ctx, root.Repo)
		if err != nil {
			return err
		}
		return emit(kctx, root, deps, snap, routes)
	}
	snap, result, err := deps.App.Endpoint(ctx, root.Repo, c.Route)
	if err != nil {
		return err
	}
	return emitEndpoint(kctx, root, deps, endpointOutput{
		snapshot: snap,
		result:   result,
		maxLines: c.MaxOutputLines,
	})
}

type endpointOutput struct {
	snapshot analyze.Snapshot
	result   retrieval.EndpointContext
	maxLines int
}

// emitEndpoint preserves complete JSON and caps only route-detail text after
// rendering, matching Pan's endpoint command. Route listings stay uncapped.
func emitEndpoint(kctx *kong.Context, root *Root, deps Deps, output endpointOutput) error {
	if root.Format == formatJSON {
		return emit(kctx, root, deps, output.snapshot, output.result)
	}
	envelope := render.NewEnvelope(commandPath(kctx), output.snapshot.Root, output.snapshot.Status, output.result, output.snapshot.Diagnostics)
	var raw bytes.Buffer
	if err := render.Write(&raw, "text", envelope); err != nil {
		return err
	}
	_, err := deps.Out.Write([]byte(formatBoundedText(raw.String(), output.maxLines, 0).Text))
	return err
}

// limitMatches caps find results in ranked order; top <= 0 lists all.
func limitMatches(matches []retrieval.SymbolMatch, top int) []retrieval.SymbolMatch {
	if top > 0 && len(matches) > top {
		return matches[:top]
	}
	return matches
}
