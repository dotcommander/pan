package cache

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

func TestInspectStreamAndLoadCaptureAgreeOnFreshManifest(t *testing.T) {
	t.Parallel()
	root, dir := t.TempDir(), t.TempDir()
	files := map[string][]byte{
		"one.go": []byte("package one\n"),
		"two.go": bytes.Repeat([]byte("// source contents\n"), 8192),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Store(dir, root, cfg, snap, snap.Stamps()); err != nil {
		t.Fatal(err)
	}
	inspected := Inspect(context.Background(), root, dir, cfg)
	loaded, status, err := LoadValidated(context.Background(), root, dir, cfg)
	if err != nil || !inspected.Usable || inspected.Stale || inspected.Reason != reasonFresh || !status.Usable || status.Stale || status.Reason != inspected.Reason {
		t.Fatalf("inspection=%#v load=%#v error=%v, want fresh parity", inspected, status, err)
	}
	if len(loaded.Captured) != len(files) {
		t.Fatalf("captured %d files, want %d", len(loaded.Captured), len(files))
	}
	for name, want := range files {
		if !bytes.Equal(loaded.Captured[name], want) {
			t.Fatalf("captured %s differs from source", name)
		}
	}
}

func TestInspectAndLoadPreserveCanceledStampReason(t *testing.T) {
	t.Parallel()
	root, dir := t.TempDir(), t.TempDir()
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Store(dir, root, cfg, snap, snap.Stamps()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inspected := Inspect(ctx, root, dir, cfg)
	_, status, err := LoadValidated(ctx, root, dir, cfg)
	if err == nil || !inspected.Stale || inspected.Reason != "stamp_failed" || !status.Stale || status.Reason != inspected.Reason {
		t.Fatalf("inspection=%#v load=%#v error=%v, want stale stamp_failed", inspected, status, err)
	}
}
