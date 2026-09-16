package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/review"
)

func selectionFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReviewSelectionDocumentMatchesDeterministicReport(t *testing.T) {
	t.Parallel()
	root := selectionFixtureRoot(t)
	svc := New(Deps{Config: config.Default()})
	options := ReviewOptions{Review: review.Options{Top: 5}}
	_, report, err := svc.ReviewReportWithOptions(context.Background(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	_, document, err := svc.ReviewSelectionDocument(context.Background(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if document.Summary.Selected != len(report.ReadQueue) {
		t.Fatalf("document selected=%d report rows=%d", document.Summary.Selected, len(report.ReadQueue))
	}
	selected := 0
	for _, item := range document.Items {
		if item.Decision != review.DecisionSelected {
			if item.SelectedRank != 0 {
				t.Fatalf("nonselected item %s has rank %d", item.Input.Path, item.SelectedRank)
			}
			continue
		}
		if selected >= len(report.ReadQueue) {
			t.Fatalf("document selected more rows than the report queue")
		}
		row := report.ReadQueue[selected]
		if item.SelectedRank != row.Rank || item.Input.Path != row.Path {
			t.Fatalf("selected item %d = %s, report row %d = %s", item.SelectedRank, item.Input.Path, row.Rank, row.Path)
		}
		selected++
	}
	if selected != len(report.ReadQueue) {
		t.Fatalf("selected=%d report rows=%d", selected, len(report.ReadQueue))
	}
}

func TestReviewSelectionDocumentRejectsConfiguredModelBeforeAcquisition(t *testing.T) {
	t.Parallel()
	svc := New(Deps{Config: config.Default()})
	cases := map[string]review.ModelOptions{
		"model":       {Model: "test-model"},
		"base-url":    {BaseURL: "http://127.0.0.1:9"},
		"api-key-env": {APIKeyEnv: "PAN_TEST_API_KEY"},
		"local":       {Local: true},
		"no-cache":    {NoCache: true},
		"cache-dir":   {CacheDir: t.TempDir()},
		"hashes":      {ContentHashes: map[string]string{"main.go": "sha256:test"}},
	}
	for name, model := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			missing := filepath.Join(t.TempDir(), "does-not-exist")
			_, _, err := svc.ReviewSelectionDocument(context.Background(), missing, ReviewOptions{Model: model})
			if err == nil {
				t.Fatal("configured model option accepted")
			}
			if !strings.Contains(err.Error(), "model options") {
				t.Fatalf("error = %v, want pre-acquisition model rejection", err)
			}
		})
	}
}

func TestReviewSelectionDocumentPerformsNoSnapshotOrModelCacheWrites(t *testing.T) {
	t.Parallel()
	root := selectionFixtureRoot(t)
	cacheDir := t.TempDir()
	svc := New(Deps{Config: config.Default(), SnapshotSource: "auto", CacheDir: cacheDir})
	_, document, err := svc.ReviewSelectionDocument(context.Background(), root, ReviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if document.Summary.Candidates == 0 {
		t.Fatalf("empty selection population: %#v", document.Summary)
	}
	if entries := cacheDirEntries(t, cacheDir); len(entries) != 0 {
		t.Fatalf("cold preview wrote cache entries: %v", entries)
	}
	if _, _, warmErr := svc.CacheWarm(context.Background(), root, cacheDir); warmErr != nil {
		t.Fatal(warmErr)
	}
	warm := cacheDirEntries(t, cacheDir)
	if len(warm) == 0 {
		t.Fatal("warm produced no cache entries")
	}
	snap, _, err := svc.ReviewSelectionDocument(context.Background(), root, ReviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status.Snapshot == nil || snap.Status.Snapshot.Source != "cache" {
		t.Fatalf("warm preview snapshot source = %#v, want cache", snap.Status.Snapshot)
	}
	if after := cacheDirEntries(t, cacheDir); !equalStrings(warm, after) {
		t.Fatalf("warm preview changed cache entries:\n before %v\n after  %v", warm, after)
	}
}

func cacheDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		entries = append(entries, relative)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
