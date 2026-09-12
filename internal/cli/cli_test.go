package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/config"
)

func newTestDeps(out io.Writer) cli.Deps {
	cfg := config.Config{MaxFiles: 100, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, OutputBudget: 1024, CommandTimeout: time.Second}
	return cli.Deps{App: app.New(app.Deps{Config: cfg}), Out: out}
}

func basicGoRepo() string {
	return filepath.Join("..", "..", "testdata", "basic-go")
}

func TestScanOverviewJSON(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	service := app.New(app.Deps{Config: config.Config{MaxFiles: 100, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, OutputBudget: 1024, CommandTimeout: time.Second}})
	root := filepath.Join("..", "..", "testdata", "basic-go")
	err := cli.Run(context.Background(), []string{"--repo", root, "--format", "json", "scan", "overview"}, cli.Deps{App: service, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"schema_version": "pan/v1"`)) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestContextEvalEnvelopeIncludesSnapshotMetadata(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	deps := newTestDeps(&out)
	snap, err := deps.App.Snapshot(context.Background(), basicGoRepo())
	if err != nil {
		t.Fatal(err)
	}
	cases := filepath.Join(t.TempDir(), "cases.jsonl")
	line := []byte(`{"schema":"pan.context-eval-case/v1","case_id":"main","snapshot_id":"` + snap.Status.Snapshot.ID + `","request":"main","token_budget":4096,"expected_paths":["main.go"],"provenance":"observed test","classification":"lexical"}` + "\n")
	if err := os.WriteFile(cases, line, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "--format", "json", "context", "eval", "--cases", cases}, deps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"snapshot": {`, `"id": "` + snap.Status.Snapshot.ID + `"`, `"freshness": "verified_at_start"`} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestExitCodeMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil maps to success", err: nil, want: cli.ExitSuccess},
		{name: "config error maps to its code", err: &cli.ExitError{Code: cli.ExitConfig, Err: errors.New("bad config")}, want: cli.ExitConfig},
		{name: "unmapped error maps to failure", err: errors.New("boom"), want: cli.ExitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := cli.ExitCode(tt.err); got != tt.want {
				t.Fatalf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func newLspDeps(out io.Writer) cli.Deps {
	cfg := config.Default()
	cfg.MaxFiles = 100
	cfg.MaxFileBytes = 1024
	cfg.MaxTotalBytes = 4096
	cfg.MaxNodes = 20
	cfg.OutputBudget = 1024
	cfg.CommandTimeout = time.Second
	return cli.Deps{App: app.New(app.Deps{Config: cfg}), Out: out}
}

func TestContextLspStatusIsImplemented(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	deps := newLspDeps(&out)
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "--format", "json", "context", "lsp", "status"}, deps); err != nil {
		t.Fatal(err)
	}
	if code := cli.ExitCode(nil); code != cli.ExitSuccess {
		t.Fatalf("exit code = %d, want %d", code, cli.ExitSuccess)
	}
	for _, want := range []string{
		`"command": [`,
		`"status"`,
		`"servers"`,
		`"gopls"`,
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestBriefAliasRunsContextBriefWiring(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	args := []string{"--repo", basicGoRepo(), "--format", "json", "brief", "trace startup"}
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"intent": "trace startup"`, `"detail": "compact"`, `"omitted_fields"`} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestImpactAliasRunsFlowImpactWiring(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	args := []string{"--repo", basicGoRepo(), "--format", "json", "impact", "main"}
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"selector": "main"`)) {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestValidationRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "negative top", args: []string{"scan", "files", "--top", "-1"}},
		{name: "zero budget", args: []string{"brief", "--budget", "0"}},
		{name: "zero depth", args: []string{"flow", "calls", "main", "--depth", "0"}},
		{name: "unknown format", args: []string{"--format", "yaml", "version"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := cli.Run(context.Background(), tt.args, newTestDeps(&out))
			if err == nil {
				t.Fatalf("args %v must fail validation", tt.args)
			}
			if code := cli.ExitCode(err); code != cli.ExitFailure {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, cli.ExitFailure, err)
			}
		})
	}
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"version"},
		{"--version"},
		{"-v"},
	} {
		var out bytes.Buffer
		if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
			t.Fatalf("args %v failed: %v", args, err)
		}
		if got := out.String(); !bytes.HasPrefix([]byte(got), []byte("pan v")) || !bytes.Contains([]byte(got), []byte("schema pan/v1")) {
			t.Fatalf("unexpected version output for %v: %q", args, got)
		}
	}
}
