package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
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
	assertReviewBriefExcludesWorkspace(t, snap, result)
}

func assertReviewBriefExcludesWorkspace(t *testing.T, snap analyze.Snapshot, result map[string]any) {
	t.Helper()
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

func TestReviewBriefExplicitWorkspaceRelevance(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeReviewBriefFile(t, root, ".gitignore", ".work/\n")
	writeReviewBriefFile(t, root, "go.mod", "module example.com/relevance\n\ngo 1.26\n")
	writeReviewBriefFile(t, root, "internal/payments.go", "package internal\nfunc Payments() {}\n")
	writeReviewBriefFile(t, root, ".work/payments.go", "package scratch\nfunc PaymentsProposal() {}\n")
	for i := range 200 {
		writeReviewBriefFile(t, root, fmt.Sprintf(".work/noise/%03d.go", i), "package scratch\nfunc Noise() {}\n")
	}
	for i := range 5 {
		writeReviewBriefFile(t, root, fmt.Sprintf("internal/aaa%03d.go", i), "package internal\nfunc Noise() {}\n")
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	cfg := config.Default()
	cfg.MaxFiles = 20
	service := New(Deps{Config: cfg, SnapshotSource: "live"})
	snap, result, err := service.ReviewBriefWithOptions(context.Background(), root, AuditOptions{Limit: 2, Intent: "payments", IncludePaths: []string{".work/./payments.go", ".work/payments.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Status.Complete {
		t.Fatalf("irrelevant scratch consumed bounds: %#v", snap.Status)
	}
	for _, file := range snap.Files {
		if strings.HasPrefix(file.Path, ".work/") && file.Path != ".work/payments.go" {
			t.Fatalf("unselected scratch admitted: %s", file.Path)
		}
	}
	queue := result["read_queue"].([]review.ReadItem)
	if len(queue) != 2 {
		t.Fatalf("queue = %#v", queue)
	}
	wanted := map[string]string{".work/payments.go": "explicit-include", "internal/payments.go": "intent-match"}
	for _, item := range queue {
		reason, ok := wanted[item.Path]
		if !ok || !slices.Contains(item.Why, reason) || item.EvidenceID == "" {
			t.Fatalf("missing relevance provenance: %#v", item)
		}
		delete(wanted, item.Path)
	}
	if len(wanted) != 0 {
		t.Fatalf("missing paths: %v", wanted)
	}
	ordinary, _, err := service.ReviewBriefWithOptions(context.Background(), root, AuditOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.Status.Snapshot.ID == snap.Status.Snapshot.ID {
		t.Fatal("selection did not change cache identity")
	}
	for _, file := range ordinary.Files {
		if file.Path == ".work/payments.go" {
			t.Fatal("selection persisted")
		}
	}
}

func TestReviewBriefRejectsInvalidIncludePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeReviewBriefFile(t, root, "source.go", "package source\n")
	if err := os.Symlink(filepath.Join(root, "source.go"), filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "../outside.go", filepath.Join(root, "source.go"), ".", "missing.go", "link.go", "outside/file.go"} {
		t.Run(path, func(t *testing.T) {
			_, _, err := New(Deps{Config: config.Default()}).ReviewBriefWithOptions(context.Background(), root, AuditOptions{IncludePaths: []string{path}})
			if err == nil {
				t.Fatalf("accepted include path %q", path)
			}
		})
	}
}
