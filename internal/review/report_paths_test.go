package review

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestComposeKeepsSourceCompatibleFilesystemFallbackPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeReportPathFixture(t, root, "go.mod", []byte("module example.com/fixture\n"))
	writeReportPathFixture(t, root, "main.go", []byte("package main\n"))
	writeReportPathFixture(t, root, "go.sum", []byte("module checksum\n"))
	writeReportPathFixture(t, root, "vendor/example/dep.go", []byte("package example\n"))
	paths := ReportPaths(root, []analyze.File{
		{Path: "go.mod", Size: 27},
		{Path: "main.go", Size: 13},
		{Path: "go.sum", Size: 16},
		{Path: "vendor/example/dep.go", Size: 16},
	})
	if !slices.Equal(paths, []string{"go.mod", "main.go"}) {
		t.Fatalf("report paths = %v", paths)
	}
	report := Compose(Packets{Paths: paths}, 0)
	if len(report.ReadQueue) != 2 {
		t.Fatalf("read queue = %#v", report.ReadQueue)
	}
	for _, item := range report.ReadQueue {
		if item.Score != 0 || !slices.Equal(item.Why, []string{"low-signal"}) {
			t.Fatalf("fallback item = %#v", item)
		}
	}
}

func TestReportPathsRejectsBinaryAndInvalidUTF8(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	valid := []byte("module example.com/fixture\n")
	writeReportPathFixture(t, root, "go.mod", valid)
	writeReportPathFixture(t, root, "binary.txt", []byte("text\x00more"))
	writeReportPathFixture(t, root, "invalid.txt", []byte{'x', 0xff})
	paths := ReportPaths(root, []analyze.File{
		{Path: "go.mod", Size: int64(len(valid))},
		{Path: "binary.txt", Size: int64(len("text\x00more"))},
		{Path: "invalid.txt", Size: 2},
	})
	if !slices.Equal(paths, []string{"go.mod"}) {
		t.Fatalf("report paths = %v", paths)
	}
}

func writeReportPathFixture(t *testing.T, root, path string, content []byte) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
