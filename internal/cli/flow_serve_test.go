package cli

import (
	"path/filepath"
	"testing"
)

func TestFlowServeResolvePathsExplicit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.yaml")
	got, err := (FlowServeCmd{Paths: []string{path}}).resolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != path {
		t.Fatalf("resolved paths = %q, want %q", got, []string{path})
	}
}

func TestBrowserTarget(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "diagram.html")
	for _, target := range []string{file, "http://127.0.0.1:7777/", "https://example.test/diagram"} {
		got, err := browserTarget(target)
		if err != nil || got != target {
			t.Fatalf("browserTarget(%q) = %q, %v", target, got, err)
		}
	}
	for _, target := range []string{"", "relative.html", "ftp://example.test/", "http://"} {
		if _, err := browserTarget(target); err == nil {
			t.Fatalf("browserTarget(%q) succeeded", target)
		}
	}
}
