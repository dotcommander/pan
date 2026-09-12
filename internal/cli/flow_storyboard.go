package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
	"github.com/dotcommander/pan/internal/pipeline/auditpacket"
	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/storyboard"
)

// FlowStoryboardCmd scans a repository and presents its lifecycle as a compact
// storyboard. Command help execution is disabled unless explicitly requested.
type FlowStoryboardCmd struct {
	Path           string        `arg:"" optional:"" help:"Repository path (default: --repo)."`
	Output         string        `name:"output" short:"o" help:"Write a document to this path; - writes a raw document to stdout."`
	DocumentFormat string        `name:"document-format" default:"text" enum:"text,json,html" help:"Document format for --output."`
	View           string        `name:"view" default:"summary" help:"Storyboard view: summary, full, stores, or command=NAME."`
	Review         string        `name:"review" help:"Pipeline spec to review against."`
	Compare        string        `name:"compare" help:"Pipeline spec to compare against."`
	CommandHelp    string        `name:"command-help" default:"off" enum:"off,static,execute" help:"Command help mode; execute runs the target binary."`
	AuditJSON      bool          `name:"audit-json" help:"Emit an audit packet as JSON."`
	AuditMarkdown  bool          `name:"audit-markdown" help:"Emit an audit packet as Markdown."`
	RefreshSpec    bool          `name:"refresh-spec" help:"Refresh the cached scan spec when stale."`
	RefreshAfter   time.Duration `name:"refresh-after" default:"24h" help:"Cached spec age that triggers --refresh-spec."`
	Open           bool          `name:"open" short:"O" help:"Open an HTML document after writing it to --output."`
	MaxPhases      int           `name:"max-phases" default:"9" help:"Maximum phases; must be non-negative."`
	MaxStages      int           `name:"max-stages" default:"7" help:"Maximum stages per phase; must be non-negative."`
}

// Validate rejects incompatible output selections before scanning.
func (c FlowStoryboardCmd) Validate() error {
	if err := scan.ValidateConfig(scan.Config{MaxPhases: c.MaxPhases, MaxStages: c.MaxStages}); err != nil {
		return err
	}
	if c.Open && (c.Output == "" || c.Output == "-" || c.DocumentFormat != "html" || c.AuditJSON || c.AuditMarkdown) {
		return errors.New("--open requires --output with an HTML file path")
	}
	if c.AuditJSON && c.AuditMarkdown {
		return errors.New("--audit-json and --audit-markdown are mutually exclusive")
	}
	switch {
	case c.View == "", c.View == storyboard.TextViewFull, c.View == storyboard.TextViewSummary, c.View == storyboard.TextViewStores, strings.HasPrefix(c.View, "command=") && strings.TrimSpace(strings.TrimPrefix(c.View, "command=")) != "":
		return nil
	default:
		return fmt.Errorf("unsupported --view %q: use summary, full, stores, or command=NAME", c.View)
	}
}

// Run executes `pan flow storyboard`.
func (c FlowStoryboardCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	if c.Output == "-" && root.Format == formatJSON {
		return errors.New("raw document stdout cannot be combined with --format json")
	}
	target := root.Repo
	if c.Path != "" {
		target = c.Path
	}
	absRoot, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	result, err := deps.App.PipelineStoryboard(ctx, target, app.StoryboardOptions{
		MaxPhases: c.MaxPhases, MaxStages: c.MaxStages, Review: c.Review, Compare: c.Compare,
		CommandHelp: c.CommandHelp, RefreshSpec: c.RefreshSpec, RefreshAfter: c.RefreshAfter,
	})
	if err != nil {
		return err
	}
	sb, provenance := result.Storyboard, result.HelpProvenance
	if c.AuditJSON || c.AuditMarkdown {
		packet := auditpacket.Build(ctx, sb, absRoot, auditpacket.Options{HelpProvenance: provenance, CommandHelpExecuted: provenance == "executed"})
		var body []byte
		if c.AuditJSON {
			body, err = auditpacket.RenderJSON(packet)
			body = append(body, '\n')
		} else {
			body = auditpacket.RenderMarkdown(packet)
		}
		if err != nil {
			return fmt.Errorf("render audit packet: %w", err)
		}
		c.DocumentFormat = "audit"
		return c.writeDocument(kctx, root, deps, absRoot, body)
	}
	body, err := storyboardDocument(sb, c.DocumentFormat, c.View)
	if err != nil {
		return err
	}
	if c.Output == "" {
		return emitResult(kctx, root, deps, absRoot, sb)
	}
	return c.writeDocument(kctx, root, deps, absRoot, body)
}

func storyboardDocument(sb storyboard.Storyboard, format, view string) ([]byte, error) {
	switch format {
	case "text":
		return storyboard.RenderTextWithOptions(sb, storyboard.TextOptions{View: view}), nil
	case formatJSON:
		data, err := json.MarshalIndent(sb, "", "  ")
		return append(data, '\n'), err
	case "html":
		return storyboard.RenderHTML(sb, storyboard.TextOptions{View: view})
	default:
		return nil, fmt.Errorf("unsupported document format %q", format)
	}
}

func (c FlowStoryboardCmd) writeDocument(kctx *kong.Context, root *Root, deps Deps, repository string, body []byte) error {
	if c.Output == "-" {
		if _, err := io.Copy(deps.Out, bytes.NewReader(body)); err != nil {
			return fmt.Errorf("write storyboard document: %w", err)
		}
		return nil
	}
	if c.Output == "" {
		return emitResult(kctx, root, deps, repository, map[string]any{formatKey: c.DocumentFormat, "storyboard": string(body)})
	}
	if err := atomicfile.Write(c.Output, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", c.Output, err)
	}
	if c.Open {
		path, pathErr := filepath.Abs(c.Output)
		if pathErr != nil {
			return fmt.Errorf("resolve output: %w", pathErr)
		}
		openBrowser(path)
	}
	return emitResult(kctx, root, deps, repository, map[string]any{outputKey: c.Output, formatKey: c.DocumentFormat})
}
