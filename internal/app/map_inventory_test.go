package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/codemap"
	"github.com/dotcommander/pan/internal/config"
)

func TestMapExcludesWorkspaceBeforeAnalysisBounds(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		".gitignore":       ".work/\nscratch/\n.hidden/\n",
		"go.mod":           "module example.com/mapinventory\n\ngo 1.26\n",
		"main.go":          "package main\nfunc Main() {}\n",
		"untracked/new.go": "package untracked\nfunc New() {}\n",
		".hidden/owned.go": "package hidden\nfunc Owned() {}\n",
		"scratch/local.go": "package scratch\nfunc Local() {}\n",
		".work/one.go":     "package work\nfunc One() {}\n",
		".work/two.go":     "package work\nfunc Two() {}\n",
		"excluded.go":      "package main\nfunc Excluded() {}\n",
	} {
		writeReviewBriefFile(t, root, name, content)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-f", ".hidden/owned.go"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, source := range []string{"live", ""} {
		t.Run("source="+source, func(t *testing.T) {
			cfg := config.Default()
			cfg.MaxFiles = 5
			cfg.Exclude = append(cfg.Exclude, "excluded.go")
			svc := New(Deps{Config: cfg, SnapshotSource: source, CacheDir: t.TempDir()})
			opts := codemap.Options{Tokens: 10000}
			snap, result, err := svc.Map(context.Background(), root, opts)
			if err != nil {
				t.Fatal(err)
			}
			if !snap.Status.Complete {
				t.Fatalf("workspace artifacts consumed analysis bounds: %#v", snap.Status)
			}
			for _, file := range snap.Files {
				if strings.HasPrefix(file.Path, ".work/") || strings.HasPrefix(file.Path, "scratch/") || file.Path == "excluded.go" {
					t.Errorf("excluded file reached analysis: %q", file.Path)
				}
			}
			structured, err := svc.MapStructured(context.Background(), root, opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"main.go", "untracked/new.go", ".hidden/owned.go"} {
				if !strings.Contains(result.Text, want) {
					t.Errorf("text map missing repository source %q: %s", want, result.Text)
				}
				found := false
				for _, file := range structured.Files {
					found = found || file.Path == want
				}
				if !found {
					t.Errorf("structured map missing repository source %q", want)
				}
			}
			if structured.Totals.Files != result.TotalFiles {
				t.Errorf("map inventories differ: structured=%d text=%d", structured.Totals.Files, result.TotalFiles)
			}
		})
	}
}
