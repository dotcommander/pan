package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestFlowBlastEnvelopeAndMarkdownDocument(t *testing.T) {
	t.Parallel()
	repo, err := filepath.Abs(basicGoRepo())
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(t.TempDir(), "pipeline.yaml")
	content := "title: Demo\nroot: " + repo + "\nphases:\n  - name: Start\n    files: [cmd/demo/main.go]\n  - name: Serve\n    files: [internal/service/service.go]\n"
	if err := os.WriteFile(specPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runJSON(t, []string{"--format", "json", "flow", "blast", "cmd/demo/main.go", "--spec", specPath})
	for _, want := range []string{`"impact"`, `"direct_phases"`, `"downstream_phases"`, `"severity"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("blast envelope missing %q:\n%s", want, got)
		}
	}
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"flow", "blast", "Start", "--spec", specPath, "--markdown", "-o", "-"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "# Impact Analysis: `Start`") {
		t.Fatalf("markdown document = %s", out.String())
	}
	out.Reset()
	if err := cli.Run(context.Background(), []string{"flow", "blast", "Start", "--spec", specPath, "--text", "-o", "-"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "IMPACT BLAST RADIUS: Start") {
		t.Fatalf("text document = %s", out.String())
	}
}

func TestFlowBlastOutputPathDefaultsToTextDocument(t *testing.T) {
	t.Parallel()
	repo, err := filepath.Abs(basicGoRepo())
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(t.TempDir(), "pipeline.yaml")
	if writeErr := os.WriteFile(specPath, []byte("title: Demo\nroot: "+repo+"\nphases:\n  - name: Start\n    files: [cmd/demo/main.go]\n"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	output := filepath.Join(t.TempDir(), "impact.txt")
	if runErr := cli.Run(context.Background(), []string{"flow", "blast", "Start", "--spec", specPath, "-o", output}, newTestDeps(&bytes.Buffer{})); runErr != nil {
		t.Fatal(runErr)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(data), "IMPACT BLAST RADIUS: Start") {
		t.Fatalf("default text document: %v\n%s", err, data)
	}
}
