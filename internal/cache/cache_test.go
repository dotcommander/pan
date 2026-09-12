package cache

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

func TestInspectDetectsWholeTreeChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.go"), []byte("package one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stamps := snap.Stamps()
	cacheDir := t.TempDir()
	if _, err := Store(cacheDir, root, cfg, snap, stamps); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(context.Background(), root, cacheDir, cfg); got.Stale || got.Reason != "fresh" {
		t.Fatalf("initial status = %#v, want fresh", got)
	}
	if err := os.WriteFile(filepath.Join(root, "two.go"), []byte("package two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(context.Background(), root, cacheDir, cfg); !got.Stale || got.Reason != "tracked_file_added" {
		t.Fatalf("added file status = %#v, want tracked_file_added", got)
	}
	if err := os.Remove(filepath.Join(root, "two.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "one.go")); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(context.Background(), root, cacheDir, cfg); !got.Stale || got.Reason != "tracked_file_missing" {
		t.Fatalf("removed file status = %#v, want tracked_file_missing", got)
	}
}

// TestInspectDetectsSizeChange pins the size freshness signal: a rewrite
// with a different length is reported size_changed even when the recorded
// modification time is pinned back, so the signal attributes to size
// alone.
func TestInspectDetectsSizeChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "one.go")
	if err := os.WriteFile(path, []byte("package one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stamps := snap.Stamps()
	before, ok := stamps["one.go"]
	if !ok {
		t.Fatalf("stamps = %v, want one.go", stamps)
	}
	cacheDir := t.TempDir()
	if _, err := Store(cacheDir, root, cfg, snap, stamps); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package one // longer now\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime, before.ModTime); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(context.Background(), root, cacheDir, cfg); !got.Stale || got.Reason != "size_changed" {
		t.Fatalf("size-change status = %#v, want stale size_changed", got)
	}
}

// TestInspectDetectsMTimeChange pins the mtime freshness signal: an
// otherwise unchanged file whose modification time shifts well past
// sub-second equality is reported mtime_changed.
func TestInspectDetectsMTimeChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "one.go")
	if err := os.WriteFile(path, []byte("package one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stamps := snap.Stamps()
	before, ok := stamps["one.go"]
	if !ok {
		t.Fatalf("stamps = %v, want one.go", stamps)
	}
	cacheDir := t.TempDir()
	if _, err := Store(cacheDir, root, cfg, snap, stamps); err != nil {
		t.Fatal(err)
	}
	shifted := before.ModTime.Add(2 * time.Hour)
	if err := os.Chtimes(path, shifted, shifted); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(context.Background(), root, cacheDir, cfg); !got.Stale || got.Reason != "mtime_changed" {
		t.Fatalf("mtime-change status = %#v, want stale mtime_changed", got)
	}
}

func TestLoadValidatedDetectsSameSizeSameMTimeContentChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "one.go")
	original := []byte("package one\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stamp := snap.Stamps()["one.go"]
	cacheDir := t.TempDir()
	if _, err := Store(cacheDir, root, cfg, snap, snap.Stamps()); err != nil {
		t.Fatal(err)
	}
	changed := []byte("package two\n")
	if len(changed) != len(original) {
		t.Fatal("test fixture must preserve size")
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp.ModTime, stamp.ModTime); err != nil {
		t.Fatal(err)
	}
	_, status, err := LoadValidated(context.Background(), root, cacheDir, cfg)
	if err == nil || !status.Stale || status.Reason != "content_changed" {
		t.Fatalf("LoadValidated status=%#v err=%v, want stale content_changed", status, err)
	}
}

func TestInspectRejectsAnalyzerRevisionMismatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.go"), []byte("package one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	entryPath, err := Store(cacheDir, root, cfg, snap, snap.Stamps())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	entry.AnalyzerRevision = "older/v1"
	data, err = json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(context.Background(), root, cacheDir, cfg); got.Usable || got.Reason != "analyzer_revision_mismatch" {
		t.Fatalf("status = %#v", got)
	}
}
