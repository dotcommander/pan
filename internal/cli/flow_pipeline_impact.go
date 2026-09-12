package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

// FlowBlastCmd analyzes pipeline-level blast radius without changing the
// existing flow impact call-graph command.
type FlowBlastCmd struct {
	Target   string `arg:"" help:"File path, stage name, store name, or symbol to analyze."`
	Spec     string `name:"spec" short:"s" help:"Pipeline spec name or path; defaults to pan.yaml, cached scan, then auto-scan."`
	Text     bool   `name:"text" help:"Emit the bare text impact document."`
	Markdown bool   `name:"markdown" help:"Emit the bare Markdown impact document."`
	JSON     bool   `name:"json" help:"Emit the bare JSON impact document."`
	Output   string `name:"output" short:"o" default:"-" help:"Write a bare --text, --markdown, or --json document to this path; - writes stdout."`
}

// Validate checks document mode and destination selection.
func (c FlowBlastCmd) Validate() error {
	if c.Target == "" {
		return errors.New("target is required (file, stage, store, or symbol)")
	}
	if documentModeCount(c.Text, c.Markdown, c.JSON) > 1 {
		return errors.New("--text, --markdown, and --json are mutually exclusive")
	}
	if c.Output == "" {
		return errors.New("--output must be a path or -")
	}
	return nil
}

// Run executes `pan flow blast`.
func (c FlowBlastCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	impactResult, err := deps.App.PipelineImpact(ctx, c.Target, c.Spec, repoOverride(kctx, root))
	specPath, sourceRoot, result := impactResult.SpecPath, impactResult.SourceRoot, impactResult.Analysis
	if err != nil {
		return err
	}
	text := c.Text
	if documentModeCount(c.Text, c.Markdown, c.JSON) == 0 && c.Output != "-" {
		text = true
	}
	if !text && !c.Markdown && !c.JSON {
		return emitResult(kctx, root, deps, sourceRoot, map[string]any{specKey: specPath, "impact": result})
	}
	var body []byte
	format := "text"
	switch {
	case text:
		body = []byte(result.Text())
	case c.Markdown:
		body = []byte(result.Markdown())
	default:
		body, err = result.JSON()
		if err == nil {
			body = append(body, '\n')
		}
		format = formatJSON
	}
	if err != nil {
		return fmt.Errorf("marshal impact document: %w", err)
	}
	if c.Output == "-" {
		if _, copyErr := io.Copy(deps.Out, bytes.NewReader(body)); copyErr != nil {
			return fmt.Errorf("write impact to stdout: %w", copyErr)
		}
		return nil
	}
	if writeErr := atomicfile.Write(c.Output, body, 0o644); writeErr != nil {
		return fmt.Errorf("write %s: %w", c.Output, writeErr)
	}
	return emitResult(kctx, root, deps, sourceRoot, map[string]any{specKey: specPath, outputKey: c.Output, formatKey: format})
}

func documentModeCount(modes ...bool) int {
	count := 0
	for _, mode := range modes {
		if mode {
			count++
		}
	}
	return count
}
