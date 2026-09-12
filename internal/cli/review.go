package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/eval"
	"github.com/dotcommander/pan/internal/review"
)

// ReviewReportCmd is `pan review report`: the composed deterministic audit
// report over every scan packet, plus a merged read queue ranking where to
// start reading. By default the report rides the standard versioned
// envelope; --markdown and --json select a bare report body for humans and
// machines, --cull appends the deterministic cull ledger, and --output
// routes the body to a file.
type ReviewReportCmd struct {
	Top       int      `name:"top" default:"25" help:"Maximum read-queue entries; 0 lists all (bounded at 100)."`
	MaxBytes  int64    `name:"max-bytes" default:"1048576" help:"Maximum bytes read from each report source file."`
	Days      int      `name:"days" default:"30" help:"Git history window in days."`
	Patterns  string   `name:"patterns" type:"path" help:"Local JSON catalog of custom risk patterns."`
	Focus     string   `name:"focus" help:"Case-insensitive regular expression over selected rows."`
	Include   []string `name:"include" sep:"," help:"Path glob to include; repeat or comma-separate."`
	Exclude   []string `name:"exclude" sep:"," help:"Path glob to exclude; repeat or comma-separate."`
	Inventory string   `name:"inventory" help:"Canonical review lane to retain."`
	WhyTop    int      `name:"why-top" help:"Include rationale for this many selected rows."`
	Model     string   `name:"model" help:"Optional OpenAI-compatible model for report scoring."`
	BaseURL   string   `name:"base-url" help:"OpenAI-compatible provider base URL for --model."`
	APIKeyEnv string   `name:"api-key-env" help:"Environment variable holding the provider API key."`
	Local     bool     `name:"local" help:"Use the documented loopback model profile."`
	NoCache   bool     `name:"no-cache" help:"Bypass the bounded persisted model score cache."`
	CacheDir  string   `name:"cache-dir" type:"path" help:"Directory for persisted model score cache."`
	Markdown  bool     `name:"markdown" help:"Emit the report body as Markdown instead of the envelope."`
	JSON      bool     `name:"json" help:"Emit the bare pan.review-report/v1 document instead of the envelope."`
	Summary   bool     `name:"summary" help:"Emit the compact summary format."`
	Cull      bool     `name:"cull" help:"Append the deterministic cull ledger separating production rows from test, docs, generated, and low-signal lanes."`
	Output    string   `name:"output" short:"o" aliases:"out" default:"-" help:"Write the report body to this path; - writes stdout."`
}

// Validate rejects conflicting format flags and an empty output path.
func (c ReviewReportCmd) Validate() error {
	if err := validateTop(c.Top); err != nil {
		return err
	}
	switch {
	case c.Markdown && c.JSON:
		return errors.New("--markdown and --json are mutually exclusive")
	case c.Days <= 0:
		return errors.New("--days must be positive")
	case c.MaxBytes <= 0:
		return errors.New("--max-bytes must be positive")
	}
	if _, err := (review.ModelOptions{Model: c.Model, BaseURL: c.BaseURL, APIKeyEnv: c.APIKeyEnv, Local: c.Local, NoCache: c.NoCache, CacheDir: c.CacheDir}).Resolve(); err != nil {
		return err
	}
	if err := (review.Options{Top: c.Top, Focus: c.Focus, Include: c.Include, Exclude: c.Exclude, Inventory: c.Inventory, WhyTop: c.WhyTop}).Validate(); err != nil {
		return err
	}
	if c.Output == "" {
		return errors.New("--output must be a path or -")
	}
	return nil
}

// Run executes `pan review report`.
func (c ReviewReportCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	options := app.ReviewOptions{Review: review.Options{Top: c.Top, Focus: c.Focus, Include: c.Include, Exclude: c.Exclude, Inventory: c.Inventory, WhyTop: c.WhyTop}, Days: c.Days, MaxBytes: c.MaxBytes, Patterns: c.Patterns, Model: review.ModelOptions{Model: c.Model, BaseURL: c.BaseURL, APIKeyEnv: c.APIKeyEnv, Local: c.Local, NoCache: c.NoCache, CacheDir: c.CacheDir}}
	snap, report, err := deps.App.ReviewReportWithOptions(ctx, root.Repo, options)
	if err != nil {
		return err
	}
	if c.Cull {
		report.CullLedger = review.BuildCullLedger(report.ReadQueue)
	}
	if !c.Markdown && !c.JSON && !c.Summary && c.Output == "-" {
		return emit(kctx, root, deps, snap, report)
	}
	body, err := c.reportBody(snap.Root, report)
	if err != nil {
		return err
	}
	format := "markdown"
	if c.JSON {
		format = formatJSON
	}
	if c.Output == "-" {
		_, err = deps.Out.Write(body)
		return err
	}
	if err := writeFileBody(c.Output, body); err != nil {
		return err
	}
	return emitResult(kctx, root, deps, snap.Root, map[string]any{outputKey: c.Output, "format": format})
}

func (c ReviewReportCmd) reportBody(root string, report review.Report) ([]byte, error) {
	switch {
	case c.Summary && !c.JSON:
		return []byte(review.RenderSummary(root, report)), nil
	case c.Summary:
		body, err := json.MarshalIndent(review.NewSummary(report), "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode review summary: %w", err)
		}
		return append(body, '\n'), nil
	case c.Markdown:
		return []byte(review.RenderMarkdown(root, report)), nil
	default:
		return review.NewDocument(c.Top, report).Bytes()
	}
}

// writeFileBody writes one report body to path with conservative permissions.
func writeFileBody(path string, body []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write report body: %w", err)
	}
	return nil
}

// ReviewEvalCmd is `pan review eval`: evaluate explicit local outcome
// ledgers against explicit local report documents. It reads only the named
// paths, never contacts a provider, and never inspects a repository.
type ReviewEvalCmd struct {
	Reports  []string `name:"report" required:"" sep:"," help:"Report document path from 'review report --json'; repeat or comma-separate."`
	Outcomes string   `name:"outcomes" required:"" type:"path" help:"Outcome JSONL ledger path."`
	Markdown bool     `name:"markdown" help:"Emit privacy-minimal Markdown instead of JSON."`
	JSON     bool     `name:"json" help:"Emit the versioned pan.eval/v1 JSON document."`
	Output   string   `name:"output" short:"o" default:"-" help:"Write the evaluation to this path; - writes stdout."`
}

// Validate requires exactly one output format and non-empty paths.
func (c ReviewEvalCmd) Validate() error {
	if c.JSON == c.Markdown {
		return errors.New("review eval requires exactly one of --json or --markdown")
	}
	if c.Output == "" {
		return errors.New("--output must be a path or -")
	}
	for _, report := range c.Reports {
		if report == "" {
			return errors.New("--report paths must not be empty")
		}
	}
	return nil
}

// Run executes `pan review eval`.
func (c ReviewEvalCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	if err := rejectEvalOutputAlias(c.Output, append([]string{c.Outcomes}, c.Reports...)); err != nil {
		return err
	}
	result, err := eval.Evaluate(ctx, c.Outcomes, c.Reports)
	if err != nil {
		return err
	}
	var body []byte
	if c.Markdown {
		body = []byte(eval.RenderMarkdown(result))
	} else {
		body, err = json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encode evaluation: %w", err)
		}
		body = append(body, '\n')
	}
	if c.Output == "-" {
		_, err = deps.Out.Write(body)
		return err
	}
	if err := writeFileBody(c.Output, body); err != nil {
		return err
	}
	return nil
}

// rejectEvalOutputAlias refuses an output path that aliases one of the
// evaluation inputs, so a run can never destroy the ledger or reports it is
// reading.
func rejectEvalOutputAlias(out string, inputs []string) error {
	if out == "-" {
		return nil
	}
	outputPath, err := filepath.Abs(filepath.Clean(out))
	if err != nil {
		return fmt.Errorf("resolve evaluation output: %w", err)
	}
	outputInfo, outputErr := os.Stat(outputPath)
	if outputErr != nil && !os.IsNotExist(outputErr) {
		return fmt.Errorf("stat evaluation output: %w", outputErr)
	}
	for _, input := range inputs {
		inputPath, err := filepath.Abs(filepath.Clean(input))
		if err != nil {
			return fmt.Errorf("resolve evaluation input: %w", err)
		}
		if inputPath == outputPath {
			return errors.New("evaluation output aliases an input")
		}
		if outputErr != nil {
			continue
		}
		inputInfo, err := os.Stat(inputPath)
		if err == nil && os.SameFile(outputInfo, inputInfo) {
			return errors.New("evaluation output aliases an input")
		}
	}
	return nil
}
