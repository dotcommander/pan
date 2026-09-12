package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareRepositoryTargetRejectsAggregateParent(t *testing.T) {
	root := t.TempDir()
	makeRepositoryMarker(t, filepath.Join(root, "pan"), false)
	makeRepositoryMarker(t, filepath.Join(root, "ctxgo"), true)

	target := &Root{Repo: root}
	err := prepareRepositoryTarget(target, nil, "")
	if err == nil {
		t.Fatal("expected aggregate parent rejection")
	}
	for _, want := range []string{"contains 2 Git repositories", "ctxgo", "pan", "--all", "--standalone"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestPrepareRepositoryTargetResolvesContainingWorktree(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	makeRepositoryMarker(t, repo, true)
	nested := filepath.Join(repo, "internal", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	target := &Root{Repo: nested}
	if err := prepareRepositoryTarget(target, nil, ""); err != nil {
		t.Fatal(err)
	}
	if target.Repo != repo {
		t.Fatalf("Repo = %q, want %q", target.Repo, repo)
	}
}

func TestPrepareRepositoryTargetExplicitModes(t *testing.T) {
	parent := t.TempDir()
	makeRepositoryMarker(t, filepath.Join(parent, "repo"), false)
	for _, test := range []struct {
		name string
		root Root
		args []string
	}{
		{name: "all repositories", root: Root{Repo: parent, AllRepos: true}},
		{name: "standalone", root: Root{Repo: parent, Standalone: true}},
		{name: "explicit repo", root: Root{Repo: parent}, args: []string{"--repo", parent}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := prepareRepositoryTarget(&test.root, test.args, ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareRepositoryTargetRejectsConflictingModes(t *testing.T) {
	err := prepareRepositoryTarget(&Root{Repo: t.TempDir(), AllRepos: true, Standalone: true}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want mutually exclusive flags", err)
	}
}

func makeRepositoryMarker(t *testing.T, root string, file bool) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".git")
	if file {
		if err := os.WriteFile(marker, []byte("gitdir: elsewhere\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}
}
