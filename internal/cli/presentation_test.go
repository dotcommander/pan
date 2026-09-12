package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/scan"
)

func presentationTestDeps(out io.Writer) Deps {
	cfg := config.Config{MaxFiles: 100, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, OutputBudget: 1024, CommandTimeout: time.Second}
	return Deps{App: app.New(app.Deps{Config: cfg}), Out: out}
}

func presentationTestRepo() string {
	return filepath.Join("..", "..", "testdata", "basic-go")
}

func TestRootHelpIsCurated(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := writeRootHelp(&out, terminalStyle{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Repository intelligence for coding agents.",
		"Understand\n",
		"Evaluate\n",
		"Improve\n",
		"pan commands",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("root help missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "context lsp hover") {
		t.Fatalf("root help leaked the exhaustive command tree:\n%s", got)
	}
}

func TestBarePanRendersDashboardWithoutANSIWhenRedirected(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--repo", presentationTestRepo()}, presentationTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"PAN / basic-go",
		"Fast repository intelligence for coding agents",
		"START HERE",
		"pan scan risks",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("redirected dashboard contains ANSI escapes: %q", got)
	}
}

func TestBarePanDoesNotAnalyzeRepository(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	deps := presentationTestDeps(&out)
	deps.LoadConfig = func() (config.Config, error) {
		t.Fatal("bare pan must not load configuration")
		return config.Config{}, nil
	}
	if err := Run(context.Background(), []string{"--repo", missing}, deps); err != nil {
		t.Fatalf("bare pan must not touch the repository: %v", err)
	}
	if !strings.Contains(out.String(), "PAN / does-not-exist") {
		t.Fatalf("dashboard did not name target: %s", out.String())
	}
}

func TestSemanticStyleCanBeDisabled(t *testing.T) {
	t.Parallel()
	if got := (terminalStyle{}).paint(ansiRed, "failed"); got != "failed" {
		t.Fatalf("disabled style = %q", got)
	}
	if got := (terminalStyle{enabled: true}).paint(ansiRed, "failed"); got != ansiRed+"failed"+ansiReset {
		t.Fatalf("enabled style = %q", got)
	}
}

func TestWriteDoctorRendersDiagnosticDetails(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	report := scan.DoctorReport{Analysis: scan.DoctorHealth{
		Complete: true,
		DiagnosticDetails: []analyze.Diagnostic{
			{Level: "warning", Message: "parse failed", Location: &analyze.Location{Path: "broken.go", Line: 7}},
			{Level: "warning", Message: "missing optional parser"},
		},
	}}
	if err := writeDoctor(&out, analyze.Snapshot{Status: analyze.Status{Complete: true}}, report); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Diagnostic [warning] broken.go:7 parse failed",
		"Diagnostic [warning] missing optional parser",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestCommandCatalogExposesAdvancedCommands(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"commands"}, presentationTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"context lsp hover", "improve refactor", "review report"} {
		if !strings.Contains(got, want) {
			t.Fatalf("catalog missing %q:\n%s", want, got)
		}
	}
}
