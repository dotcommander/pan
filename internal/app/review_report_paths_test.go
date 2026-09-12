package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/review"
)

func TestReviewReportIncludesFilesystemFallbackRows(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(Deps{Config: config.Default()})
	_, report, err := svc.ReviewReport(context.Background(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	item, found := findReportPath(report.ReadQueue, "go.mod")
	if !found || item.Score != 0 || !slices.Equal(item.Why, []string{"low-signal"}) {
		t.Fatalf("go.mod fallback = %#v, found=%t", item, found)
	}
}

func findReportPath(items []review.ReadItem, path string) (review.ReadItem, bool) {
	for _, item := range items {
		if item.Path == path {
			return item, true
		}
	}
	return review.ReadItem{}, false
}
