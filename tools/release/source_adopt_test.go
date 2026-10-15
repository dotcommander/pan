package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newAdoptFixture builds an offline git repository pair (worktree plus
// bare origin) that mirrors an externally published release: main and the
// version tag are pushed, and the release commit carries the target
// binaryVersion.
func newAdoptFixture(t *testing.T, version string) *releaseWorkflow {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "origin.git")
	work := filepath.Join(dir, "work")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", work}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	writeVersion := func(version string) {
		t.Helper()
		path := filepath.Join(work, "internal", "cli", "cli.go")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := "package cli\n\nconst binaryVersion = \"v" + version + "\"\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	git("config", "user.email", "release-test@example.com")
	git("config", "user.name", "Release Test")
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")
	git("config", "url."+bare+".insteadOf", "https://github.com/dotcommander/pan")
	git("remote", "add", "origin", "https://github.com/dotcommander/pan")
	writeVersion("0.1.0")
	git("add", "-A")
	git("commit", "-q", "-m", "prior")
	writeVersion(version)
	git("add", "-A")
	git("commit", "-q", "-m", "chore(release): set version "+version)
	git("tag", "-a", "v"+version, "-m", "Release v"+version)
	git("push", "-q", "origin", "main", "refs/tags/v"+version)

	return &releaseWorkflow{
		proc:    newProcess(io.Discard, io.Discard),
		root:    work,
		version: version,
		tag:     "v" + version,
		state: releaseState{
			Version:   version,
			Tag:       "v" + version,
			Completed: map[string]string{},
		},
	}
}

func TestAdoptSourceStateAdoptsPublishedRelease(t *testing.T) {
	t.Parallel()
	w := newAdoptFixture(t, "0.2.0")
	if err := w.adoptSourceState(context.Background()); err != nil {
		t.Fatalf("adoptSourceState: %v", err)
	}
	head, err := w.proc.capture(context.Background(), w.root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if w.state.ReleaseCommit != head {
		t.Errorf("ReleaseCommit = %s, want HEAD %s", w.state.ReleaseCommit, head)
	}
	if w.state.PreviousVersion != "0.1.0" {
		t.Errorf("PreviousVersion = %q, want 0.1.0", w.state.PreviousVersion)
	}
	if w.state.SourceRemoteBase != head {
		t.Errorf("SourceRemoteBase = %s, want %s", w.state.SourceRemoteBase, head)
	}
}

func TestAdoptSourceStateRejectsMissingRemoteTag(t *testing.T) {
	t.Parallel()
	w := newAdoptFixture(t, "0.2.0")
	for _, args := range [][]string{
		{"tag", "-d", w.tag},
		{"push", "-q", "origin", ":refs/tags/" + w.tag},
	} {
		cmd := exec.Command("git", append([]string{"-C", w.root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	err := w.adoptSourceState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refs/tags/"+w.tag) {
		t.Fatalf("adoptSourceState error = %v, want missing tag failure", err)
	}
}

func TestAdoptSourceStateRejectsAdvancedMain(t *testing.T) {
	t.Parallel()
	w := newAdoptFixture(t, "0.2.0")
	path := filepath.Join(w.root, "docs.md")
	if err := os.WriteFile(path, []byte("post-release note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-q", "-m", "docs"},
		{"push", "-q", "origin", "main"},
	} {
		cmd := exec.Command("git", append([]string{"-C", w.root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	err := w.adoptSourceState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "origin/main is") {
		t.Fatalf("adoptSourceState error = %v, want origin/main drift", err)
	}
}

func TestAdoptSourceStateRejectsVersionMismatch(t *testing.T) {
	t.Parallel()
	w := newAdoptFixture(t, "0.2.0")
	for _, args := range [][]string{
		{"tag", "-a", "v0.3.0", "-m", "Release v0.3.0"},
		{"push", "-q", "origin", "refs/tags/v0.3.0"},
	} {
		cmd := exec.Command("git", append([]string{"-C", w.root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	mismatched := &releaseWorkflow{
		proc:    w.proc,
		root:    w.root,
		version: "0.3.0",
		tag:     "v0.3.0",
		state:   releaseState{Version: "0.3.0", Tag: "v0.3.0", Completed: map[string]string{}},
	}
	err := mismatched.adoptSourceState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "carries binaryVersion") {
		t.Fatalf("adoptSourceState error = %v, want binaryVersion mismatch", err)
	}
}

func TestAdoptSourceTagValidatesTarget(t *testing.T) {
	t.Parallel()
	w := newAdoptFixture(t, "0.2.0")
	if err := w.adoptSourceState(context.Background()); err != nil {
		t.Fatalf("adoptSourceState: %v", err)
	}
	if err := w.adoptSourceTag(context.Background()); err != nil {
		t.Fatalf("adoptSourceTag: %v", err)
	}
	w.state.ReleaseCommit = "0000000000000000000000000000000000000000"
	if err := w.adoptSourceTag(context.Background()); err == nil {
		t.Fatal("adoptSourceTag accepted a tag that misses the release commit")
	}
}

func TestAdoptSourceCommitRequiresClean(t *testing.T) {
	t.Parallel()
	w := newAdoptFixture(t, "0.2.0")
	if err := w.adoptSourceCommit(context.Background()); err != nil {
		t.Fatalf("adoptSourceCommit on clean tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.root, "docs.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.adoptSourceCommit(context.Background()); err == nil {
		t.Fatal("adoptSourceCommit accepted a dirty worktree")
	}
}

func TestAdoptSourcePublishValidatesRemote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newAdoptFixture(t, "0.2.0")
	if err := w.adoptSourceState(ctx); err != nil {
		t.Fatalf("adoptSourceState: %v", err)
	}
	if err := w.adoptSourcePublish(ctx); err != nil {
		t.Fatalf("adoptSourcePublish: %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.root, "docs.md"), []byte("drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-q", "-m", "docs"},
		{"push", "-q", "origin", "main"},
	} {
		cmd := exec.Command("git", append([]string{"-C", w.root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	if err := w.adoptSourcePublish(ctx); err == nil {
		t.Fatal("adoptSourcePublish accepted remote drift")
	}
}
