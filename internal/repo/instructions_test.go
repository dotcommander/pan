package repo_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/repo"
)

func writeTree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("# instructions\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestInstructionsScopedAndSorted(t *testing.T) {
	t.Parallel()
	dir := writeTree(t,
		"AGENTS.md",
		"sub/AGENTS.md",
		"sub/CLAUDE.md",
		"deep/nest/AGENTS.md",
		"docs/guide.md",
		"vendor/AGENTS.md",
		"node_modules/pkg/AGENTS.md",
	)
	paths, truncated, err := repo.Instructions(dir, repo.Scope{Exclude: []string{"vendor", "node_modules"}, MaxFiles: 100})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AGENTS.md", "deep/nest/AGENTS.md", "sub/AGENTS.md", "sub/CLAUDE.md"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if truncated {
		t.Fatal("truncated = true, want false")
	}
}

func TestInstructionsBoundStopsDiscovery(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, "AGENTS.md", "sub/AGENTS.md", "sub/CLAUDE.md", "deep/AGENTS.md")
	paths, truncated, err := repo.Instructions(dir, repo.Scope{MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want exactly 2 entries", paths)
	}
	if !slices.IsSorted(paths) {
		t.Fatalf("paths = %v, want sorted", paths)
	}
	if !truncated {
		t.Fatal("truncated = false, want true after hitting the bound")
	}
}

func TestInstructionsDefaultBoundWhenUnset(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, "AGENTS.md", "sub/CLAUDE.md")
	paths, truncated, err := repo.Instructions(dir, repo.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want 2 entries", paths)
	}
	if truncated {
		t.Fatal("truncated = true, want false under the default bound")
	}
}

func TestInstructionsSkipsSymlinkedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(target, []byte("# real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	paths, _, err := repo.Instructions(dir, repo.Scope{MaxFiles: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths = %v, want none: symlinked instruction files must not be trusted", paths)
	}
}

func TestInstructionsRejectsInvalidRoot(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Instructions(file, repo.Scope{}); err == nil {
		t.Fatal("expected error for file root")
	}
	if _, _, err := repo.Instructions(filepath.Join(t.TempDir(), "missing"), repo.Scope{}); err == nil {
		t.Fatal("expected error for missing root")
	}
}
