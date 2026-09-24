package app

import (
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/retrieval"
)

func TestCallSelectionRejectsAmbiguityAndKeepsOnlyExactTargets(t *testing.T) {
	t.Parallel()
	a := analyze.Symbol{Name: "Run", Kind: "method", Location: analyze.Location{Path: "a.go", Line: 2}, EndLine: 3}
	b := analyze.Symbol{Name: "Run", Kind: "method", Location: analyze.Location{Path: "b.go", Line: 2}, EndLine: 3}
	snap := analyze.Snapshot{
		Files:   []analyze.File{{Path: "a.go"}, {Path: "b.go"}, {Path: "caller.go"}},
		Symbols: []analyze.Symbol{a, b},
		Edges: []analyze.Edge{
			{From: "Caller", To: "Run", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "caller.go", Line: 1}, Target: &a.Location},
			{From: "Unknown", To: "Run", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "caller.go", Line: 2}},
		},
	}
	ranked := ranking.Rank(snap, "", ranking.Options{})
	if _, err := selectCallSymbol(ranked, "Run"); err == nil || !strings.Contains(err.Error(), "symbol:") {
		t.Fatalf("ambiguous selector error = %v", err)
	}
	match, err := selectCallSymbol(ranked, retrieval.SymbolHandle("a.go", a))
	if err != nil || match == nil || match.File != "a.go" {
		t.Fatalf("handle selection = %+v, %v", match, err)
	}
	selected := selectedCallEdges(snap, match.Handle, match)
	if len(selected) != 1 || selected[0].Target == nil || selected[0].Target.Path != "a.go" {
		t.Fatalf("handle selected unrelated call: %+v", selected)
	}
}
