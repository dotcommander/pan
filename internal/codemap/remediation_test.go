package codemap

import (
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinesUseCapturedGenerationAndExplicitlyOmitMissingSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("func Live() {}"), 0600); err != nil {
		t.Fatal(err)
	}
	ranked := []ranking.RankedFile{{Path: "a.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{{Name: "Captured", Exported: true, Location: analyze.Location{Line: 1}}}}}
	got := Build(ranked, Options{Root: root, Mode: ModeLines, Captured: map[string][]byte{"a.go": []byte("func Captured() {}")}})
	if !strings.Contains(got.Text, "func Captured()") || strings.Contains(got.Text, "func Live()") {
		t.Fatalf("generation escaped: %s", got.Text)
	}
	got = Build(ranked, Options{Root: root, Mode: ModeLines})
	if !strings.Contains(got.Text, "captured source unavailable") || strings.Contains(got.Text, "func Live()") {
		t.Fatalf("missing capture fell back: %s", got.Text)
	}
}
