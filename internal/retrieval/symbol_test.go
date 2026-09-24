package retrieval

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestSymbolCallersFiltersTestsAndHonorsLimit(t *testing.T) {
	t.Parallel()
	match := SymbolMatch{File: "service.go", Symbol: analyze.Symbol{Name: "Run"}}
	snapshot := analyze.Snapshot{Edges: []analyze.Edge{
		{From: "Production", To: "Run", Kind: edgeKindCalls, Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "service.go", Line: 2}},
		{From: "TestRun", To: "Run", Kind: edgeKindCalls, Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "service_test.go", Line: 4}},
		{From: "Another", To: "Run", Kind: edgeKindCalls, Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "other.go", Line: 6}},
	}}

	withoutTests := symbolCallers(snapshot, match, false, 1)
	if len(withoutTests) != 1 || withoutTests[0].Symbol != "Another" {
		t.Fatalf("callers without tests = %#v", withoutTests)
	}
	withTests := symbolCallers(snapshot, match, true, 0)
	if len(withTests) != 3 {
		t.Fatalf("unlimited callers with tests = %#v", withTests)
	}
}

func TestSymbolCallersKeepsTypedIdentityWithoutLexicalFanout(t *testing.T) {
	t.Parallel()
	a := analyze.Symbol{Name: "Run", Kind: "method", Location: analyze.Location{Path: "a.go", Line: 2}}
	b := analyze.Symbol{Name: "Run", Kind: "method", Location: analyze.Location{Path: "b.go", Line: 2}}
	snapshot := analyze.Snapshot{
		Symbols: []analyze.Symbol{a, b},
		Edges: []analyze.Edge{
			{From: "A", To: "Run", Kind: edgeKindCalls, Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "caller.go", Line: 1}, Target: &a.Location},
			{From: "B", To: "Run", Kind: edgeKindCalls, Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "caller.go", Line: 2}, Target: &b.Location},
			{From: "Unknown", To: "Run", Kind: edgeKindCalls, Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "caller.go", Line: 3}},
		},
	}
	callers := symbolCallers(snapshot, SymbolMatch{File: "a.go", Symbol: a}, true, 0)
	if len(callers) != 1 || callers[0].Symbol != "A" || callers[0].Confidence != analyze.ConfidenceConfirmed {
		t.Fatalf("callers = %+v, want only confirmed A", callers)
	}
}
