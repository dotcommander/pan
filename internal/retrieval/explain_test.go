package retrieval

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestExplainPrioritizesTargetAndReturnsSymbols(t *testing.T) {
	targetSymbols := []analyze.Symbol{
		{Name: "Run", Kind: "function", Exported: true},
		{Name: "helper", Kind: "function"},
	}
	ranked := []ranking.RankedFile{
		{Path: "other.go", Language: languageGo, Symbols: makeSymbols(80)},
		{Path: "target.go", Language: languageGo, Symbols: targetSymbols, Components: map[string]int{"symbols": 4}},
	}
	got, err := Explain(ranked, "target.go", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if got.DetailLevel != 2 || got.SymbolCount != len(targetSymbols) || len(got.Symbols) != len(targetSymbols) {
		t.Fatalf("target was not fully explained: %#v", got)
	}
}

func TestExplainNoSymbolEvidenceIsExplicit(t *testing.T) {
	ranked := []ranking.RankedFile{{Path: "target.go", Language: languageGo}}
	got, err := Explain(ranked, "target.go", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if got.SymbolCount != 0 || got.Symbols != nil || got.OmittedReason != "no symbol evidence was available for this file" {
		t.Fatalf("missing symbol evidence should be explicit: %#v", got)
	}
}

func TestExplainTinyBudgetIsExplicitAndBounded(t *testing.T) {
	ranked := []ranking.RankedFile{{Path: "target.go", Language: languageGo, Symbols: []analyze.Symbol{{Name: "Run"}}}}
	got, err := Explain(ranked, "target.go", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.SymbolCount != 1 || got.Symbols != nil || got.OmittedReason == "" {
		t.Fatalf("tiny explain should be explicit and bounded: %#v", got)
	}
}

func makeSymbols(n int) []analyze.Symbol {
	symbols := make([]analyze.Symbol, n)
	for i := range symbols {
		symbols[i] = analyze.Symbol{Name: "Symbol", Exported: true}
	}
	return symbols
}
