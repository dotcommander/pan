package bench

import (
	"math"
	"slices"
	"testing"
)

func TestScorePerfectRanking(t *testing.T) {
	t.Parallel()
	gold := []string{"a.go", "b.go"}
	ranked := []string{"a.go", "c.go", "b.go"}
	metrics := Score(ranked, gold)
	if metrics.RecallAt1 != 0.5 || metrics.RecallAt5 != 1 || metrics.RecallAt10 != 1 || metrics.RecallAt20 != 1 {
		t.Fatalf("recall windows = %+v", metrics)
	}
	if metrics.MRR != 1 {
		t.Fatalf("MRR = %v, want 1", metrics.MRR)
	}
	if metrics.NDCGAt10 <= 0.9 || metrics.NDCGAt10 > 1 {
		t.Fatalf("NDCG@10 = %v, want in (0.9, 1]", metrics.NDCGAt10)
	}
}

func TestScoreMisses(t *testing.T) {
	t.Parallel()
	metrics := Score([]string{"x.go", "y.go"}, []string{"a.go"})
	if metrics != (Metrics{}) {
		t.Fatalf("all-miss score = %+v, want zeros", metrics)
	}
	if metrics := Score([]string{"a.go"}, nil); metrics != (Metrics{}) {
		t.Fatalf("empty-gold score = %+v, want zeros", metrics)
	}
}

func TestScoreShortRankedList(t *testing.T) {
	t.Parallel()
	// A single-hit list shorter than every window still scores the smaller
	// windows correctly.
	metrics := Score([]string{"gold.go"}, []string{"gold.go"})
	if metrics.RecallAt1 != 1 || metrics.MRR != 1 || metrics.NDCGAt10 != 1 {
		t.Fatalf("single-hit score = %+v", metrics)
	}
}

func TestMeanMetrics(t *testing.T) {
	t.Parallel()
	if metrics := MeanMetrics(nil); metrics != (Metrics{}) {
		t.Fatalf("MeanMetrics(nil) = %+v, want zeros", metrics)
	}
	mean := MeanMetrics([]Metrics{{RecallAt1: 0, MRR: 0}, {RecallAt1: 1, MRR: 0.5}})
	if mean.RecallAt1 != 0.5 || !floatEq(mean.MRR, 0.25) {
		t.Fatalf("MeanMetrics = %+v", mean)
	}
}

func TestRankedPathsCap(t *testing.T) {
	t.Parallel()
	paths := []string{"a", "b", "c"}
	if got := RankedPaths(paths, 2); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("RankedPaths cap = %v", got)
	}
	if got := RankedPaths(paths, 0); !slices.Equal(got, paths) {
		t.Fatalf("RankedPaths zero cap = %v", got)
	}
	capped := RankedPaths(paths, 2)
	capped[0] = "mutated"
	if paths[0] != "a" {
		t.Fatal("RankedPaths must clone its input")
	}
}

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}
