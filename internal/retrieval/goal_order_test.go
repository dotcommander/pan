package retrieval

import (
	"fmt"
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestOrderForGoalPrioritizesExplicitNames(t *testing.T) {
	t.Parallel()
	var decoys []analyze.Symbol
	for i := range 20 {
		decoys = append(decoys, analyze.Symbol{
			Name: fmt.Sprintf("JSONRequestLine%d", i),
			Doc:  "decode JSON objects in request lines",
		})
	}
	for _, goal := range []string{
		"decodeServeObject",
		"Locate decodeServeObject: rejecting multiple JSON objects in one NDJSON JSON-RPC request line.",
		"Inspect `decodeServeObject` before changing request parsing.",
	} {
		t.Run(goal, func(t *testing.T) {
			t.Parallel()
			ranked := []ranking.RankedFile{
				{Path: "hub.go", Score: 500, Symbols: decoys, Components: map[string]int{"imports": 500}},
				{Path: "owner.go", Score: 1, Symbols: []analyze.Symbol{{Name: "decodeServeObject"}}},
				{Path: "other.go", Score: 0},
			}
			got := OrderForGoal(analyze.Snapshot{}, ranked, goal)
			wantPaths := []string{"owner.go", "hub.go", "other.go"}
			if len(got) != len(wantPaths) {
				t.Fatalf("got %d files, want %d", len(got), len(wantPaths))
			}
			for i, want := range wantPaths {
				if got[i].Path != want {
					t.Fatalf("position %d = %q, want %q", i, got[i].Path, want)
				}
			}
			if ranked[0].Path != "hub.go" || ranked[1].Path != "owner.go" || ranked[2].Path != "other.go" {
				t.Fatal("input order mutated")
			}
			if got[0].Score != 1 || got[1].Score != 500 || got[1].Components["imports"] != 500 {
				t.Fatal("structural score evidence mutated")
			}
		})
	}
}

func TestOrderForGoalPreservesOrientationWithoutEvidence(t *testing.T) {
	t.Parallel()
	for _, goal := range []string{"", "  ", "zzyyxxq"} {
		t.Run(goal, func(t *testing.T) {
			t.Parallel()
			ranked := []ranking.RankedFile{{Path: "owner_test.go", Score: 5}, {Path: "main.go", Score: 3}}
			got := OrderForGoal(analyze.Snapshot{}, ranked, goal)
			if !slices.EqualFunc(got, ranked, func(a, b ranking.RankedFile) bool {
				return a.Path == b.Path && a.Score == b.Score
			}) {
				t.Fatalf("orientation changed: %#v", got)
			}
			got[0].Path = "changed.go"
			if ranked[0].Path != "owner_test.go" {
				t.Fatal("output aliases input slice")
			}
		})
	}
	if got := OrderForGoal(analyze.Snapshot{}, nil, "decoder"); got != nil {
		t.Fatalf("nil input = %#v, want nil", got)
	}
}
