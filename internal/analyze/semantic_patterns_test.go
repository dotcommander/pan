package analyze

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/config"
)

func TestSemanticGoPatternsScopeCapturedDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for path, source := range map[string]string{
		"go.mod":              "module example.test/scope\n\ngo 1.25\n",
		"a/kept.go":           "package a\nfunc Kept() {}\n",
		"aa-excluded/skip.go": "package excluded\nfunc Skip() {}\n",
		"b/oversized.go":      "package b\n// " + strings.Repeat("x", 200) + "\n",
		"c/README.md":         "unrelated captured non-Go directory\n",
		"d/over-bound.go":     "package d\nfunc OverBound() {}\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Exclude = append(cfg.Exclude, "aa-excluded")
	cfg.MaxFileBytes = 128
	cfg.MaxFiles = 2
	snap, err := Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := semanticGoPatterns(snap.Files), []string{"./a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("patterns = %v, want %v (skipped = %v, limits = %v)", got, want, snap.Status.Skipped, snap.Status.Limits)
	}
	if !strings.Contains(strings.Join(snap.Status.Skipped, ","), "aa-excluded (excluded)") ||
		!strings.Contains(strings.Join(snap.Status.Skipped, ","), "b/oversized.go (oversized)") ||
		!strings.Contains(strings.Join(snap.Status.Limits, ","), "max_files") {
		t.Fatalf("expected excluded, oversized, and over-file-bound entries: %+v", snap.Status)
	}
}

func TestSemanticGoPatternsDeterministicAndLiteral(t *testing.T) {
	t.Parallel()
	files := []File{
		{Path: "z/second.go", Language: LanguageGo},
		{Path: "main.go", Language: LanguageGo},
		{Path: "a/first.go", Language: LanguageGo},
		{Path: "a/again.go", Language: LanguageGo},
		{Path: "wild.../unsafe.go", Language: LanguageGo},
		{Path: "../outside.go", Language: LanguageGo},
		{Path: "z/README.md", Language: LanguageUnknown},
	}
	want := []string{".", "./a", "./z"}
	if got := semanticGoPatterns(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("patterns = %v, want %v", got, want)
	}
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	if got := semanticGoPatterns(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("reordered patterns = %v, want %v", got, want)
	}
}

func TestSemanticGoCallsPreserveCrossPackageTargets(t *testing.T) {
	t.Parallel()
	snap, err := Build(context.Background(), "../../testdata/basic-go", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"main": "internal/service/service.go", "Run": "internal/service/service.go"}
	counts := make(map[string]int)
	for _, edge := range snap.Edges {
		if edge.Kind != "calls" || (edge.To != "Run" && edge.To != "helper") {
			continue
		}
		if edge.Confidence != ConfidenceConfirmed || edge.Target == nil || edge.Target.Path != want[edge.From] {
			t.Fatalf("call lost captured target or retained lexical duplicate: %+v", edge)
		}
		counts[edge.From]++
	}
	if !reflect.DeepEqual(counts, map[string]int{"main": 1, "Run": 1}) {
		t.Fatalf("confirmed call counts = %v, want main and Run once each", counts)
	}
}
