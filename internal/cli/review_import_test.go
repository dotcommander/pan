package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/dotcommander/pan/internal/cli"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewImportStdinOutsideRepository(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"ast-grep", "jscpd", "scip"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var input []byte
			switch format {
			case "ast-grep":
				input = []byte("[]")
			case "jscpd":
				input = []byte(`{"statistics":{},"duplicates":[]}`)
			case "scip":
				input = []byte{0x0a, 0}
			}
			var out bytes.Buffer
			deps := newTestDeps(&out)
			deps.In = bytes.NewReader(input)
			err := cli.Run(t.Context(), []string{"--repo", filepath.Join(t.TempDir(), "missing"), "--format", "json", "review", "import", format, "-"}, deps)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("stdout not JSON: %v %s", err, out.Bytes())
			}
			if !bytes.Contains(result["result"], []byte(`"schema":"tool-report/v1"`)) && !bytes.Contains(result["result"], []byte(`"schema": "tool-report/v1"`)) {
				t.Fatalf("missing imported result: %s", out.Bytes())
			}
		})
	}
}

func TestReviewImportFileArtifactAndFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	input := filepath.Join(dir, "matches.json")
	artifact := filepath.Join(dir, "report.json")
	if err := os.WriteFile(input, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"--format", "json", "--artifact", artifact, "review", "import", "ast-grep", input}
	if err := cli.Run(t.Context(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(artifact)
	if err != nil || !json.Valid(before) || out.Len() != 0 {
		t.Fatalf("artifact output: %v %s", err, before)
	}
	if err := os.WriteFile(input, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(t.Context(), args, newTestDeps(&out)); err == nil {
		t.Fatal("invalid input accepted")
	}
	after, err := os.ReadFile(artifact)
	if err != nil || !bytes.Equal(before, after) || out.Len() != 0 {
		t.Fatal("failure published partial output")
	}
}

func TestReviewImportBoundaryErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"review", "import", "unknown", "-"}, {"review", "import", "ast-grep", filepath.Join(t.TempDir(), "missing")}} {
		var out bytes.Buffer
		if err := cli.Run(t.Context(), args, newTestDeps(&out)); err == nil || out.Len() != 0 {
			t.Fatal("boundary error ignored or output leaked")
		}
	}
	want := errors.New("output failure")
	deps := newTestDeps(&bytes.Buffer{})
	deps.In = strings.NewReader("[]")
	deps.Out = reportFailWriter{want}
	if err := cli.Run(context.Background(), []string{"--format", "json", "review", "import", "ast-grep", "-"}, deps); !errors.Is(err, want) {
		t.Fatalf("writer failure lost: %v", err)
	}
}

type reportFailWriter struct{ err error }

func (w reportFailWriter) Write([]byte) (int, error) { return 0, w.err }
