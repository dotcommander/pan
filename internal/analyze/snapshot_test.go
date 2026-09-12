package analyze_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

const fixture = "../../testdata/basic-go"

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func boundedConfig() config.Config {
	return config.Config{
		MaxFiles:        100,
		MaxFileBytes:    4096,
		MaxTotalBytes:   1 << 20,
		MaxNodes:        100,
		MaxInstructions: 100,
		CommandTimeout:  time.Second,
		OutputBudget:    4096,
	}
}

func TestBuildExtractsSymbolsAndEdges(t *testing.T) {
	t.Parallel()
	snap, err := analyze.Build(context.Background(), fixture, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Symbols) < 3 {
		t.Fatalf("symbols = %d, want at least 3", len(snap.Symbols))
	}
	if len(snap.Edges) < 2 {
		t.Fatalf("edges = %d, want at least 2", len(snap.Edges))
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	t.Parallel()
	cfg := boundedConfig()
	first, err := analyze.Build(context.Background(), fixture, cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := analyze.Build(context.Background(), fixture, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two builds of the same tree produced different snapshots")
	}
}

func TestBuildStampsSchemaVersion(t *testing.T) {
	t.Parallel()
	snap, err := analyze.Build(context.Background(), fixture, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	if snap.SchemaVersion != analyze.SchemaVersion {
		t.Fatalf("schema version = %q, want %q", snap.SchemaVersion, analyze.SchemaVersion)
	}
}

func TestBuildExcludedDirectoriesAreScopedOut(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"main.go":               "package main\n\nfunc main() {}\n",
		"vendor/dep.go":         "package vendor\n",
		"node_modules/pkg/x.go": "package pkg\n",
	})
	cfg := boundedConfig()
	cfg.Exclude = []string{"vendor", "node_modules"}
	snap, err := analyze.Build(context.Background(), dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range snap.Files {
		paths = append(paths, f.Path)
	}
	if len(paths) != 1 || paths[0] != "main.go" {
		t.Fatalf("files = %v, want [main.go]", paths)
	}
	if !contains(snap.Status.Skipped, "vendor (excluded)") || !contains(snap.Status.Skipped, "node_modules (excluded)") {
		t.Fatalf("skipped = %v, want explicit excluded entries", snap.Status.Skipped)
	}
	if !snap.Status.Complete {
		t.Fatalf("complete = false, want true: policy skips must not flip completeness (status %+v)", snap.Status)
	}
	if len(snap.Status.Limits) != 0 {
		t.Fatalf("limits = %v, want none", snap.Status.Limits)
	}
}

func TestBuildExcludesExactFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "kept.go"), []byte("package sample\nfunc Kept() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.go"), []byte("package sample\nfunc Ignored() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Exclude = append(cfg.Exclude, "ignored.go")
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotHasSymbol(snap, "Ignored") {
		t.Fatal("exact excluded file was analyzed")
	}
	if !snapshotHasSymbol(snap, "Kept") {
		t.Fatal("non-excluded file was not analyzed")
	}
}

func TestBuildRecordsOversizedSkipAndLimit(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
		"big.go":  "package big\n\n// " + strings.Repeat("x", 120) + "\nfunc Big() {}\n",
	})
	cfg := boundedConfig()
	cfg.MaxFileBytes = 64
	snap, err := analyze.Build(context.Background(), dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(snap.Status.Skipped, "big.go (oversized)") {
		t.Fatalf("skipped = %v, want big.go (oversized)", snap.Status.Skipped)
	}
	if !contains(snap.Status.Limits, "max_file_bytes") {
		t.Fatalf("limits = %v, want max_file_bytes", snap.Status.Limits)
	}
	if snap.Status.Complete {
		t.Fatal("complete = true, want false after a bound truncated discovery")
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != "main.go" {
		t.Fatalf("files = %v, want only main.go", snap.Files)
	}
}

func TestBuildStopsAtMaxFiles(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
		"c.go": "package c\n",
	})
	cfg := boundedConfig()
	cfg.MaxFiles = 2
	snap, err := analyze.Build(context.Background(), dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(snap.Files))
	}
	if !contains(snap.Status.Limits, "max_files") {
		t.Fatalf("limits = %v, want max_files", snap.Status.Limits)
	}
	if snap.Status.Complete {
		t.Fatal("complete = true, want false after max_files truncation")
	}
}

func TestBuildCapsGraphAtMaxNodes(t *testing.T) {
	t.Parallel()
	cfg := boundedConfig()
	cfg.MaxNodes = 2
	snap, err := analyze.Build(context.Background(), fixture, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if total := len(snap.Symbols) + len(snap.Edges); total != cfg.MaxNodes {
		t.Fatalf("symbols+edges = %d, want exactly %d", total, cfg.MaxNodes)
	}
	if !contains(snap.Status.Limits, "max_nodes") {
		t.Fatalf("limits = %v, want max_nodes", snap.Status.Limits)
	}
	if snap.Status.Complete {
		t.Fatal("complete = true, want false after graph truncation")
	}
}

func TestBuildSkipsSymlinkedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	snap, err := analyze.Build(context.Background(), dir, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(snap.Status.Skipped, "link.txt (symlink)") {
		t.Fatalf("skipped = %v, want link.txt (symlink)", snap.Status.Skipped)
	}
	if !snap.Status.Complete {
		t.Fatalf("complete = false, want true: symlink skips are policy, status %+v", snap.Status)
	}
}

func TestBuildReportsParseDiagnostics(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"broken.go": "package broken\n\nfunc {{{\n",
	})
	snap, err := analyze.Build(context.Background(), dir, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Diagnostics) == 0 {
		t.Fatal("diagnostics empty, want a parse warning")
	}
	if snap.Diagnostics[0].Level != "warning" {
		t.Fatalf("level = %q, want warning", snap.Diagnostics[0].Level)
	}
	if snap.Status.Complete {
		t.Fatal("complete = true, want false after a parse failure")
	}
}

func TestBuildRejectsInvalidRoot(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := analyze.Build(context.Background(), file, boundedConfig()); err == nil {
		t.Fatal("expected error for non-directory root")
	}
}

func TestBuildHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := analyze.Build(ctx, fixture, boundedConfig())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestBuildParsesTreeSitterLanguages(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"native.c":     "#include <stdio.h>\nint greet(void) { return 1; }\n",
		"service.java": "import java.util.List; class Service { void run() {} }\n",
		"worker.py":    "import os\nclass Worker:\n    def run(self):\n        pass\n",
		"lib.rs":       "use std::fmt;\nstruct Worker;\nfn run() {}\n",
		"web.ts":       "import { thing } from './thing';\nexport class Page { run() {} }\n",
	})
	snapshot, err := analyze.Build(context.Background(), dir, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"greet", "Service", "Worker", "run", "Page"} {
		if !snapshotHasSymbol(snapshot, want) {
			t.Fatalf("symbols = %#v, missing %q", snapshot.Symbols, want)
		}
	}
	if !snapshotHasImport(snapshot, "native.c") || !snapshotHasImport(snapshot, "web.ts") {
		t.Fatalf("edges = %#v, want C and TypeScript imports", snapshot.Edges)
	}
}

func TestBuildResolvesCapturedTreeSitterReferences(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"definition.py":  "class Worker:\n    pass\n",
		"consumer.py":    "def build():\n    return Worker()\n",
		"definition.ts":  "export class Page {}\n",
		"consumer.ts":    "export function render(): Page { return new Page() }\n",
		"duplicate-a.py": "class Duplicate:\n    pass\n",
		"duplicate-b.py": "class Duplicate:\n    pass\n",
	})
	snapshot, err := analyze.Build(context.Background(), dir, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ from, to, symbol string }{
		{"consumer.py", "definition.py", "Worker"},
		{"consumer.ts", "definition.ts", "Page"},
	} {
		found := false
		for _, edge := range snapshot.Edges {
			if edge.Kind == "references" && edge.From == want.from && edge.To == want.to && edge.Symbol == want.symbol && edge.Confidence == analyze.ConfidenceSyntactic {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("edges = %#v, missing %+v", snapshot.Edges, want)
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == "references" && edge.Symbol == "Duplicate" {
			t.Fatalf("declaration was emitted as a reference: %#v", edge)
		}
	}
}

func TestBuildAddsConfirmedGoCall(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"go.mod":  "module example.test/calls\n\ngo 1.25\n",
		"main.go": "package calls\nfunc target() {}\nfunc caller() { target() }\n",
	})
	snapshot, err := analyze.Build(context.Background(), dir, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == "calls" && edge.From == "caller" && edge.To == "target" && edge.Confidence == analyze.ConfidenceConfirmed {
			return
		}
	}
	t.Fatalf("edges = %#v, want confirmed caller -> target edge", snapshot.Edges)
}

func snapshotHasSymbol(snapshot analyze.Snapshot, name string) bool {
	for _, symbol := range snapshot.Symbols {
		if symbol.Name == name {
			return true
		}
	}
	return false
}

func snapshotHasImport(snapshot analyze.Snapshot, path string) bool {
	for _, edge := range snapshot.Edges {
		if edge.Kind == "imports" && edge.From == path {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
