package bench

import (
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func testSnapshot(t *testing.T, files map[string]string) analyze.Snapshot {
	t.Helper()
	snap := analyze.Snapshot{Captured: make(map[string][]byte, len(files))}
	for path, source := range files {
		snap.Files = append(snap.Files, analyze.File{Path: path, Language: "go", Size: int64(len(source))})
		snap.Captured[path] = []byte(source)
	}
	return snap
}

func TestBM25RankPrefersMatchingContent(t *testing.T) {
	t.Parallel()
	snap := testSnapshot(t, map[string]string{
		"widget.go": "package widget\n\n// ParseWidget parses the widget input.\nfunc ParseWidget(input string) error { return nil }",
		"other.go":  "package other\n\nfunc Unrelated() {}",
	})
	ranked := BM25Rank(snap, "parse the widget input", 5)
	if len(ranked) == 0 || ranked[0] != "widget.go" {
		t.Fatalf("BM25Rank = %v, want widget.go first", ranked)
	}
}

func TestBM25RankUsesPathTokens(t *testing.T) {
	t.Parallel()
	snap := testSnapshot(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
	})
	ranked := BM25Rank(snap, "b", 2)
	if len(ranked) != 2 || ranked[0] != "b.go" {
		t.Fatalf("BM25Rank = %v, want b.go first", ranked)
	}
}

func TestBM25RankDeterministicTieBreak(t *testing.T) {
	t.Parallel()
	snap := testSnapshot(t, map[string]string{
		"z.go": "package z\nfunc Same() {}",
		"a.go": "package a\nfunc Same() {}",
	})
	first := BM25Rank(snap, "same", 2)
	second := BM25Rank(snap, "same", 2)
	if !slices.Equal(first, second) {
		t.Fatalf("BM25Rank not deterministic: %v vs %v", first, second)
	}
	if !slices.Equal(first, []string{"a.go", "z.go"}) {
		t.Fatalf("BM25Rank tie-break = %v, want path order", first)
	}
}

func TestBM25RankBounds(t *testing.T) {
	t.Parallel()
	snap := testSnapshot(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	if ranked := BM25Rank(snap, "a", 0); ranked != nil {
		t.Fatalf("BM25Rank topK=0 = %v, want nil", ranked)
	}
	if ranked := BM25Rank(snap, "a", 1); len(ranked) != 1 {
		t.Fatalf("BM25Rank topK=1 = %v", ranked)
	}
	empty := testSnapshot(t, nil)
	if ranked := BM25Rank(empty, "a", 5); ranked != nil {
		t.Fatalf("BM25Rank empty snapshot = %v, want nil", ranked)
	}
	missing := analyze.Snapshot{Files: []analyze.File{{Path: "x.go"}}}
	if ranked := BM25Rank(missing, "a", 5); ranked != nil {
		t.Fatalf("BM25Rank without captured source = %v, want nil", ranked)
	}
}
