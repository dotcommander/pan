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

func TestContextPackStdinOutsideRepository(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	deps := newTestDeps(&out)
	deps.In = strings.NewReader(`{"files":{"nested/a.json":" { \"n\": 1e400 } ","empty":""}}`)
	err := cli.Run(context.Background(), []string{"--repo", filepath.Join(t.TempDir(), "missing"), "context", "pack", "-"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Schema string `json:"schema"`
		Files  []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("machine stdout is not JSON: %v", err)
	}
	if result.Schema != "pan-pack/v1" || len(result.Files) != 2 || result.Files[1].Content != `{"n":1e400}` {
		t.Fatalf("unexpected packed output: %s", out.String())
	}
}

func TestContextPackFileAndArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	input, artifact := filepath.Join(dir, "capture.json"), filepath.Join(dir, "pack.json")
	if err := os.WriteFile(input, []byte(`{"files":{"a":"captured content"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", filepath.Join(dir, "missing"), "--artifact", artifact, "context", "pack", input}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("artifact command wrote stdout: %q", out.String())
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) || !bytes.Contains(data, []byte(`"content":"captured content"`)) {
		t.Fatalf("invalid artifact: %s", data)
	}
}

func TestContextPackErrorsDoNotPublishPartialOutput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`{"files":{"../outside":"x"}}`, `<files><file path="a">broken < source</file></files>`} {
		t.Run(input[:1], func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			deps := newTestDeps(&out)
			deps.In = strings.NewReader(input)
			artifact := filepath.Join(t.TempDir(), "pack.json")
			if err := os.WriteFile(artifact, []byte("previous artifact"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := cli.Run(context.Background(), []string{"--artifact", artifact, "context", "pack", "-"}, deps); err == nil {
				t.Fatal("invalid input accepted")
			}
			if out.Len() != 0 {
				t.Fatal("partial machine output")
			}
			data, err := os.ReadFile(artifact)
			if err != nil || string(data) != "previous artifact" {
				t.Fatal("failure replaced prior artifact")
			}
		})
	}
}
