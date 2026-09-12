package storyboard

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestMissingRefreshPreservesSourceRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module example.test/refresh\n\ngo 1.25\n",
		"main.go": "package main\nfunc main(){run()}\nfunc run(){}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "user-config", "refresh.yaml")
	result, err := refreshSpecAt(t.Context(), root, output, scan.Config{MaxPhases: 8, MaxStages: 8}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Refreshed || result.Reason != "missing" {
		t.Fatalf("unexpected refresh: %+v", result)
	}
	doc, err := spec.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Root != root {
		t.Fatalf("source root %q, want %q", doc.Root, root)
	}
	// The output directory and current process directory are both outside root.
	if resolved := spec.ProjectRootForSpec(doc, output, ""); resolved != root {
		t.Fatalf("consumer resolved %q", resolved)
	}
}
