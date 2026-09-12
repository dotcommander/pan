package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

// pipelineSpec returns the absolute fixture path for a pipeline spec under
// the repository testdata root.
func pipelineSpec(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFlowStoryboardEmitsLifecycleStoryboard(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "flow", "storyboard"})
	for _, want := range []string{`"project_name"`, `"prelude"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("storyboard output missing %q:\n%s", want, got)
		}
	}
}

func TestFlowStoryboardRejectsNegativeSizing(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"flow", "storyboard", "--max-phases=-1"}, newTestDeps(&out))
	if err == nil {
		t.Fatal("negative max phases must be rejected")
	}
	if code := cli.ExitCode(err); code != cli.ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, cli.ExitFailure)
	}
	if !strings.Contains(err.Error(), "max phases must be non-negative") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlowRenderOutputDashEmbedsHTMLPage(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"flow", "render", pipelineSpec(t, "minimal.yaml"), "--output", "-"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<!DOCTYPE html>", "Minimal"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("render output missing %q:\n%s", want, out.String())
		}
	}
}

func TestFlowRenderWritesOutputFile(t *testing.T) {
	t.Parallel()
	outPath := filepath.Join(t.TempDir(), "minimal.html")
	got := runJSON(t, []string{"--format", "json", "flow", "render", pipelineSpec(t, "minimal.yaml"), "--output", outPath})
	if !strings.Contains(got, `"output"`) {
		t.Fatalf("render output missing written path:\n%s", got)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read rendered page: %v", err)
	}
	page := string(data)
	for _, want := range []string{"<!DOCTYPE html>", "Minimal"} {
		if !strings.Contains(page, want) {
			t.Fatalf("rendered page missing %q:\n%s", want, page)
		}
	}
}

func TestFlowRenderWritesDefaultOutputInWorkingDirectory(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestFlowRenderDefaultOutputHelper$")
	cmd.Env = append(os.Environ(), "PAN_FLOW_RENDER_DEFAULT_OUTPUT_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render default output helper: %v\n%s", err, output)
	}
}

// TestFlowRenderDefaultOutputHelper runs in a subprocess so changing the
// working directory cannot race parallel CLI tests.
func TestFlowRenderDefaultOutputHelper(t *testing.T) {
	if os.Getenv("PAN_FLOW_RENDER_DEFAULT_OUTPUT_HELPER") != "1" {
		return
	}
	spec := pipelineSpec(t, "minimal.yaml")
	workdir := t.TempDir()
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--format", "json", "flow", "render", spec}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(workdir, "out", "minimal.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Minimal") || !strings.Contains(out.String(), `"output"`) {
		t.Fatalf("default render result = %s", out.String())
	}
}

func TestFlowValidateAcceptsMinimalSpec(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--format", "json", "flow", "validate", pipelineSpec(t, "minimal.yaml")})
	if !strings.Contains(got, `"status": "valid"`) {
		t.Fatalf("validate output missing valid status:\n%s", got)
	}
}

func TestFlowValidateReportsInvalidSpec(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	badSpec := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badSpec, []byte("title: Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cli.Run(context.Background(), []string{"--format", "json", "flow", "validate", badSpec}, newTestDeps(&out))
	if err == nil {
		t.Fatal("empty spec must be invalid")
	}
	if code := cli.ExitCode(err); code != cli.ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, cli.ExitFailure)
	}
	got := out.String()
	for _, want := range []string{`"status": "invalid"`, "spec must have phases or commands"} {
		if !strings.Contains(got, want) {
			t.Fatalf("validate output missing %q:\n%s", want, got)
		}
	}
}

func TestFlowReviewEmitsDriftReport(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--format", "json", "flow", "review", pipelineSpec(t, "paper.yaml")})
	for _, want := range []string{`"report"`, "# Review:", `"by_severity"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("review output missing %q:\n%s", want, got)
		}
	}
}

func TestFlowReviewStrictFailsOnHighFindings(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"flow", "review", pipelineSpec(t, "paper.yaml"), "--strict"}, newTestDeps(&out))
	if err == nil {
		t.Fatal("strict review must fail when high-severity findings exist")
	}
	if code := cli.ExitCode(err); code != cli.ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, cli.ExitFailure)
	}
	if !strings.Contains(out.String(), "# Review:") {
		t.Fatalf("strict review must still emit the report:\n%s", out.String())
	}
}

func TestFlowPipelineUsesEmbeddedRootUnlessRepoIsExplicit(t *testing.T) {
	t.Parallel()
	repo, pathErr := filepath.Abs(basicGoRepo())
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	specPath := filepath.Join(t.TempDir(), "scan.yaml")
	if err := cli.Run(context.Background(), []string{"--repo", repo, "flow", "scan", "-o", specPath}, newTestDeps(io.Discard)); err != nil {
		t.Fatal(err)
	}

	pagePath := filepath.Join(t.TempDir(), "embedded.html")
	if err := cli.Run(context.Background(), []string{"flow", "render", specPath, "--output", pagePath}, newTestDeps(io.Discard)); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "vscode://file/"+filepath.Join(repo, "cmd", "demo", "main.go")) {
		t.Fatalf("render did not use embedded root:\n%s", page)
	}

	got := runJSON(t, []string{"--format", "json", "flow", "review", specPath})
	if strings.Contains(got, "file not found: cmd/demo/main.go") {
		t.Fatalf("review did not use embedded root:\n%s", got)
	}

	override := t.TempDir()
	overriddenPage := filepath.Join(t.TempDir(), "override.html")
	if runErr := cli.Run(context.Background(), []string{"--repo", override, "flow", "render", specPath, "--output", overriddenPage}, newTestDeps(io.Discard)); runErr != nil {
		t.Fatal(runErr)
	}
	page, err = os.ReadFile(overriddenPage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "vscode://file/"+filepath.Join(override, "cmd", "demo", "main.go")) {
		t.Fatalf("render did not use explicit root:\n%s", page)
	}
}
