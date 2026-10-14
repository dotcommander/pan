package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepoWithUnstagedChange creates a committed repository with one unstaged
// modification so `git status --short` reports exactly one " M" line.
func gitRepoWithUnstagedChange(t *testing.T, root, body string) {
	t.Helper()
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(body)
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "main.go"},
		{"-c", "user.name=Pan Test", "-c", "user.email=pan@example.invalid", "commit", "-qm", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	write(body + "\nfunc changed() {}\n")
}

func TestBriefRepositoryStatePreservesStatusColumns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitRepoWithUnstagedChange(t, root, "package main\n\nfunc main() {}\n")

	state := briefRepositoryState(context.Background(), root)

	if len(state.Dirty) != 1 {
		t.Fatalf("dirty entries = %v, want exactly one", state.Dirty)
	}
	if state.Dirty[0] != " M main.go" {
		t.Fatalf("first dirty entry = %q, want %q with the leading unstaged-status column preserved", state.Dirty[0], " M main.go")
	}
	if state.Branch == "" {
		t.Fatalf("branch = %q, want a resolved branch name", state.Branch)
	}
}
