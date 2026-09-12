package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/review"
)

func TestReviewBriefSeparatesIgnoredWorkspaceSource(t *testing.T) {
	root := t.TempDir()
	writeReviewBriefFile(t, root, ".gitignore", ".work/\nscratch/\n")
	writeReviewBriefFile(t, root, "go.mod", "module example.com/reviewbrief\n\ngo 1.25\n")
	writeReviewBriefFile(t, root, "internal/service.go", "package internal\n\nfunc Run() error { return nil }\n")
	writeReviewBriefFile(t, root, "scratch/local.go", "package scratch\n\nfunc Local() {}\n")
	for _, name := range []string{"one.go", "two.go", "three.go"} {
		writeReviewBriefFile(t, root, filepath.Join(".work", "bench", name), "package bench\n\nfunc Work() {}\n")
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	cfg := config.Default()
	cfg.MaxNodes = 10
	snap, result, err := New(Deps{Config: cfg}).ReviewBriefWithOptions(context.Background(), root, AuditOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Status.Complete {
		t.Fatalf("workspace artifacts consumed analysis bounds: %#v", snap.Status)
	}
	for _, skipped := range snap.Status.Skipped {
		if strings.HasPrefix(skipped, ".work/") {
			t.Fatalf("workspace descendants leaked into skipped diagnostics: %q", skipped)
		}
	}
	for _, instruction := range snap.Instructions {
		if strings.HasPrefix(instruction, ".work/") {
			t.Fatalf("ignored workspace instruction leaked: %q", instruction)
		}
	}
	queue, ok := result["read_queue"].([]review.ReadItem)
	if !ok {
		t.Fatalf("read_queue type = %T", result["read_queue"])
	}
	for _, item := range queue {
		if strings.HasPrefix(item.Path, ".work/") || strings.HasPrefix(item.Path, "scratch/") {
			t.Fatalf("workspace artifact leaked into read queue: %#v", item)
		}
	}
	hygiene, ok := result["workspace_hygiene"].(reviewBriefHygiene)
	if !ok {
		t.Fatalf("workspace_hygiene type = %T", result["workspace_hygiene"])
	}
	if hygiene.Counts.IgnoredSource != 4 || len(hygiene.Details) == 0 {
		t.Fatalf("ignored workspace source not preserved separately: %#v", hygiene)
	}
}

func writeReviewBriefFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
