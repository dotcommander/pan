package retrieval

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestSymbolCallersFiltersTestsAndHonorsLimit(t *testing.T) {
	t.Parallel()
	match := SymbolMatch{File: "service.go", Symbol: analyze.Symbol{Name: "Run"}}
	snapshot := analyze.Snapshot{Edges: []analyze.Edge{
		{From: "Production", To: "Run", Kind: edgeKindCalls, Location: analyze.Location{Path: "service.go", Line: 2}},
		{From: "TestRun", To: "Run", Kind: edgeKindCalls, Location: analyze.Location{Path: "service_test.go", Line: 4}},
		{From: "Another", To: "Run", Kind: edgeKindCalls, Location: analyze.Location{Path: "other.go", Line: 6}},
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
