package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/pipeline/review"
)

// specKey is the result key carrying the resolved pipeline spec path.
const specKey = "spec"

// FlowCmd groups bounded execution, dependency, route, and pipeline evidence.
type FlowCmd struct {
	Entry      EntryCmd          `cmd:"" help:"Entry-point symbols."`
	Imports    ImportsCmd        `cmd:"" help:"Import dependency edges."`
	Calls      CallsCmd          `cmd:"" help:"Call edges for one symbol."`
	Impact     FlowImpactCmd     `cmd:"" help:"Direct call evidence for one symbol."`
	Blast      FlowBlastCmd      `cmd:"" help:"Pipeline blast radius for files, stages, stores, and symbols."`
	Storyboard FlowStoryboardCmd `cmd:"" help:"Scan a repository into a lifecycle storyboard."`
	Init       FlowInitCmd       `cmd:"" help:"Create a starter pipeline spec in the repository."`
	Config     FlowConfigCmd     `cmd:"" help:"Set up starter flow configuration files."`
	Scan       FlowScanCmd       `cmd:"" help:"Scan a repository into a pipeline spec."`
	Render     FlowRenderCmd     `cmd:"" help:"Render a pipeline spec as an HTML page."`
	Serve      FlowServeCmd      `cmd:"" help:"Serve pipeline diagrams with live reload."`
	Validate   FlowValidateCmd   `cmd:"" help:"Validate a pipeline spec against the repository."`
	Review     FlowReviewCmd     `cmd:"" help:"Review a pipeline spec for drift against the repository."`
	Graph      FlowGraphCmd      `cmd:"" help:"Deterministic import dependency graph."`
	Endpoint   FlowEndpointCmd   `cmd:"" help:"Route-to-handler-to-test trace."`
}

// EntryCmd is `pan flow entry`.
type EntryCmd struct{}

// Run executes `pan flow entry`.
func (EntryCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, err := deps.App.Snapshot(ctx, root.Repo)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, entrySymbols(snap.Symbols))
}

// ImportsCmd is `pan flow imports`.
type ImportsCmd struct {
	Package string `name:"package" help:"Restrict edges to one imported package."`
}

// Run executes `pan flow imports`.
func (c ImportsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, err := deps.App.Snapshot(ctx, root.Repo)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, importEdges(snap.Edges, c.Package))
}

// CallsCmd is `pan flow calls`.
type CallsCmd struct {
	Symbol string `arg:"" help:"Symbol selector."`
	Depth  int    `name:"depth" default:"2" help:"Traversal depth; must be positive."`
}

// Validate rejects non-positive traversal depths.
func (c CallsCmd) Validate() error {
	if c.Depth < 1 {
		return errors.New("--depth must be positive")
	}
	return nil
}

// Run executes `pan flow calls`.
func (c CallsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, edges, err := deps.App.Calls(ctx, root.Repo, c.Symbol, c.Depth)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, edges)
}

// impactOptions is shared by `flow impact` and the top-level `impact`
// compatibility alias so both accept identical arguments and validation.
type impactOptions struct {
	Selector string `arg:"" help:"Symbol selector."`
	Depth    int    `name:"depth" default:"2" help:"Traversal depth; must be positive."`
}

func (o impactOptions) Validate() error {
	if o.Depth < 1 {
		return errors.New("--depth must be positive")
	}
	return nil
}

// FlowImpactCmd is `pan flow impact`.
type FlowImpactCmd struct{ impactOptions }

// ImpactAliasCmd is the top-level `pan impact` compatibility alias.
type ImpactAliasCmd struct{ impactOptions }

// Run executes `pan flow impact`.
func (FlowImpactCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	return runImpact(kctx, root, deps, ctx, root.Flow.Impact.impactOptions)
}

// Run executes the top-level `pan impact` compatibility alias.
func (ImpactAliasCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	return runImpact(kctx, root, deps, ctx, root.Impact.impactOptions)
}

func runImpact(kctx *kong.Context, root *Root, deps Deps, ctx context.Context, opts impactOptions) error {
	snap, edges, err := deps.App.Calls(ctx, root.Repo, opts.Selector, opts.Depth)
	if err != nil {
		return err
	}
	result := map[string]any{"selector": opts.Selector, "edges": edges}
	return emit(kctx, root, deps, snap, result)
}

// FlowRenderCmd is `pan flow render`.
type FlowRenderCmd struct {
	Spec   string `arg:"" help:"Pipeline spec path or bare name."`
	Output string `name:"output" short:"o" help:"Write the HTML page to this path instead of embedding it in the result."`
	Open   bool   `name:"open" short:"O" help:"Open the generated HTML file in the default browser."`
}

// Validate rejects browser opening only when the rendered page remains inline.
// An omitted output path is resolved to the default file before rendering.
func (c FlowRenderCmd) Validate() error {
	if c.Open && c.Output == "-" {
		return errors.New("--open cannot be used with --output -")
	}
	return nil
}

// Run executes `pan flow render`.
func (c FlowRenderCmd) Run(kctx *kong.Context, root *Root, deps Deps, _ context.Context) error {
	output := c.Output
	if output == "" {
		output = flowRenderDefaultOutput(c.Spec)
	}
	renderOutput := output
	if output == "-" {
		renderOutput = ""
	}
	specPath, html, err := deps.App.PipelineRender(c.Spec, repoOverride(kctx, root), renderOutput, deps.In)
	if err != nil {
		return err
	}
	if output == "-" {
		return writePipelineDocument(root, deps, html)
	}
	if c.Open {
		path, pathErr := filepath.Abs(output)
		if pathErr != nil {
			return pathErr
		}
		openBrowser(path)
	}
	result := map[string]any{specKey: specPath}
	if html == nil {
		result[outputKey] = output
	} else {
		result["html"] = string(html)
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), result)
}

func flowRenderDefaultOutput(spec string) string {
	base := filepath.Base(spec)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join("out", base+".html")
}

// FlowValidateCmd is `pan flow validate`.
type FlowValidateCmd struct {
	Spec string `arg:"" help:"Pipeline spec path or bare name."`
}

// Run executes `pan flow validate`.
func (c FlowValidateCmd) Run(kctx *kong.Context, root *Root, deps Deps, _ context.Context) error {
	specPath, findings, err := deps.App.PipelineValidate(c.Spec, repoOverride(kctx, root), deps.In)
	if err != nil {
		return err
	}
	repo := displayRepo(root.Repo)
	if len(findings) == 0 {
		result := map[string]any{specKey: specPath, "status": "valid"}
		return emitResult(kctx, root, deps, repo, result)
	}
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		messages = append(messages, finding.Error())
	}
	result := map[string]any{specKey: specPath, "status": "invalid", "findings": len(findings)}
	if err := emitDiagnostics(kctx, root, deps, result, messages); err != nil {
		return err
	}
	return &ExitError{Code: ExitFailure, Err: fmt.Errorf("spec validation failed with %d finding(s)", len(findings))}
}

// FlowReviewCmd is `pan flow review`.
type FlowReviewCmd struct {
	Spec   string `arg:"" help:"Pipeline spec path or bare name."`
	Strict bool   `name:"strict" help:"Exit non-zero when high-severity or worse findings exist."`
	Output string `short:"o" help:"Write Markdown report to a file, or - for raw stdout."`
}

// Run executes `pan flow review`.
func (c FlowReviewCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	specPath, report, err := deps.App.PipelineReview(ctx, c.Spec, repoOverride(kctx, root), deps.In)
	if err != nil {
		return err
	}
	bySeverity := map[string]int{}
	for _, finding := range report.Findings {
		bySeverity[finding.Severity.String()]++
	}
	result := map[string]any{
		specKey:       specPath,
		"findings":    len(report.Findings),
		"by_severity": bySeverity,
		"report":      report.Markdown(),
	}
	if err := c.emit(kctx, root, deps, report.Markdown(), result); err != nil {
		return err
	}
	if c.Strict && report.HasSeverity(review.High) {
		count := report.CountBySeverity(review.High) + report.CountBySeverity(review.Critical)
		return &ExitError{Code: ExitFailure, Err: fmt.Errorf("review found %d high+ findings", count)}
	}
	return nil
}

// repoOverride passes --repo through only when the caller explicitly chose it.
// Otherwise pipeline specs may use their embedded root.
func repoOverride(kctx *kong.Context, root *Root) string {
	if _, ok := os.LookupEnv("PAN_REPO"); ok {
		return root.Repo
	}
	for _, path := range kctx.Path {
		if path.Flag != nil && path.Flag.Name == "repo" {
			return root.Repo
		}
	}
	return ""
}

func entrySymbols(symbols []analyze.Symbol) []analyze.Symbol {
	var entries []analyze.Symbol
	for _, symbol := range symbols {
		if symbol.Name == "main" {
			entries = append(entries, symbol)
		}
	}
	return entries
}

func importEdges(edges []analyze.Edge, pkg string) []analyze.Edge {
	var result []analyze.Edge
	for _, edge := range edges {
		if edge.Kind == "imports" && (pkg == "" || edge.To == pkg) {
			result = append(result, edge)
		}
	}
	return result
}
