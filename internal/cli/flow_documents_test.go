package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestFlowDocumentsFromStdin(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"validate", "review", "render"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			deps := newTestDeps(&out)
			deps.In = strings.NewReader("title: Streamed\nphases:\n  - name: Process\n    stages:\n      - label: run\n")
			args := []string{"flow", command, "-"}
			if command != "validate" {
				args = append(args, "-o", "-")
			}
			if err := cli.Run(context.Background(), args, deps); err != nil {
				t.Fatal(err)
			}
			if command == "render" && !strings.Contains(out.String(), "<!DOCTYPE html>") {
				t.Fatalf("expected HTML: %s", out.String())
			}
			if command == "review" && !strings.HasPrefix(out.String(), "# Review:") {
				t.Fatalf("expected Markdown: %s", out.String())
			}
		})
	}
}

func TestVersionBuildProvenance(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"version", "--json"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "schema_version", "go_version", "module_path", "vcs_revision", "vcs_modified"} {
		if _, ok := result[key]; !ok {
			t.Errorf("missing provenance field %s", key)
		}
	}
}

func TestRenderOpenRejectsInlineOutput(t *testing.T) {
	t.Parallel()
	if err := (cli.FlowRenderCmd{Open: true, Output: "-"}).Validate(); err == nil {
		t.Error("accepted open with inline output")
	}
	if err := (cli.FlowRenderCmd{Open: true}).Validate(); err != nil {
		t.Fatalf("default output should support open: %v", err)
	}
}

func TestFlowReviewMarkdownFile(t *testing.T) {
	t.Parallel()
	output := filepath.Join(t.TempDir(), "review.md")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"flow", "review", pipelineSpec(t, "paper.yaml"), "-o", output}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("# Review:")) {
		t.Fatalf("expected Markdown: %s", data)
	}
}
