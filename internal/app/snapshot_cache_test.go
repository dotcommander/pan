package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/config"
)

func TestSnapshotAutoReusesExplicitlyWarmedGeneration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("captured instruction\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cacheDir := t.TempDir()
	live := New(Deps{Config: cfg, SnapshotSource: "live", CacheDir: cacheDir})
	warmed, _, err := live.CacheWarm(context.Background(), root, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	auto := New(Deps{Config: cfg, SnapshotSource: "auto", CacheDir: cacheDir})
	cached, err := auto.Snapshot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Status.Snapshot.Source != "cache" {
		t.Fatalf("source=%q, want cache", cached.Status.Snapshot.Source)
	}
	if cached.Status.Snapshot.ID != warmed.Status.Snapshot.ID {
		t.Fatalf("cache id=%q live id=%q", cached.Status.Snapshot.ID, warmed.Status.Snapshot.ID)
	}
	got, ok := cached.Source("AGENTS.md")
	if !ok || string(got) != "captured instruction\n" {
		t.Fatalf("captured instruction=%q ok=%v", got, ok)
	}
}

func TestSnapshotAutoReportsLiveFallbackWithoutChangingIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	live, err := New(Deps{Config: cfg, SnapshotSource: "live"}).Snapshot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	auto, err := New(Deps{Config: cfg, SnapshotSource: "auto", CacheDir: t.TempDir()}).Snapshot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if auto.Status.Snapshot.ID != live.Status.Snapshot.ID {
		t.Fatalf("auto id=%q live id=%q", auto.Status.Snapshot.ID, live.Status.Snapshot.ID)
	}
	if len(auto.Diagnostics) == 0 || !strings.Contains(auto.Diagnostics[len(auto.Diagnostics)-1].Message, "missing_cache") {
		t.Fatalf("diagnostics=%v, want missing_cache fallback", auto.Diagnostics)
	}
}
