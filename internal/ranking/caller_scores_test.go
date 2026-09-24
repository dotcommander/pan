package ranking

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestCallerScoresDoNotCreditSameNamedUncalledFiles(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{
		Files: []analyze.File{{Path: "a.go", Language: "go"}, {Path: "b.go", Language: "go"}, {Path: "caller.go", Language: "go"}},
		Symbols: []analyze.Symbol{
			{Name: "Run", Location: analyze.Location{Path: "a.go", Line: 2}},
			{Name: "Run", Location: analyze.Location{Path: "b.go", Line: 2}},
		},
		Edges: []analyze.Edge{
			{From: "Use", To: "Run", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "caller.go", Line: 3}, Target: &analyze.Location{Path: "a.go", Line: 2}},
			{From: "Other", To: "Run", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "caller.go", Line: 4}},
		},
	}
	ranked := Rank(snap, "", Options{})
	if componentFor(ranked, "a.go", ComponentCallers) == 0 || componentFor(ranked, "b.go", ComponentCallers) != 0 {
		t.Fatalf("caller components = %#v, want only a.go credited", ranked)
	}
	for _, file := range ranked {
		if file.Path == "a.go" && file.Confidence != analyze.ConfidenceConfirmed {
			t.Fatalf("resolved target confidence = %s", file.Confidence)
		}
	}
}

func TestCallEdgeBonusRequiresExactCapturedTarget(t *testing.T) {
	t.Parallel()
	ranked := []RankedFile{
		{Path: "a.go", ImportedBy: 2, Symbols: []analyze.Symbol{{Name: "Run", Exported: true, Location: analyze.Location{Path: "a.go", Line: 2}}}, Components: map[string]int{}},
		{Path: "b.go", ImportedBy: 2, Symbols: []analyze.Symbol{{Name: "Run", Exported: true, Location: analyze.Location{Path: "b.go", Line: 2}}}, Components: map[string]int{}},
	}
	ApplyCallEdgeBonus(ranked, []analyze.Edge{{Kind: "calls", To: "Run", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "caller.go"}}}, 2, true)
	if ranked[0].Components[ComponentCallers] != 0 || ranked[1].Components[ComponentCallers] != 0 {
		t.Fatalf("bare name gained confirmed credit: %#v", ranked)
	}
	ApplyCallEdgeBonus(ranked, []analyze.Edge{{Kind: "calls", To: "Run", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "caller.go"}, Target: &analyze.Location{Path: "b.go", Line: 2}}}, 2, true)
	for _, file := range ranked {
		if (file.Path == "a.go" && file.Components[ComponentCallers] != 0) || (file.Path == "b.go" && file.Components[ComponentCallers] == 0) {
			t.Fatalf("confirmed credit went to wrong declaration: %#v", ranked)
		}
	}
}
