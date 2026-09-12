package ranking

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestApplySymbolReferenceBonusPromotesReferencedNonGoSymbol(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "model.ts"), []byte("export class Widget {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "use.ts"), []byte("new Widget()"), 0o600); err != nil {
		t.Fatal(err)
	}
	ranked := []RankedFile{{Path: "model.ts", Language: "typescript", Symbols: []analyze.Symbol{{Name: "Widget", Exported: true}}, Components: map[string]int{}}, {Path: "use.ts", Language: "typescript", Components: map[string]int{}}}
	ApplySymbolReferenceBonus(root, ranked)
	if ranked[0].Path != "model.ts" || ranked[0].Components["symbol_refs"] == 0 {
		t.Fatalf("ranked = %#v", ranked)
	}
}

func TestApplyCallEdgeBonusFiltersTestEvidence(t *testing.T) {
	t.Parallel()
	ranked := []RankedFile{{Path: "a.go", ImportedBy: 2, Symbols: []analyze.Symbol{{Name: "Alpha", Exported: true}}, Components: map[string]int{}}}
	edges := []analyze.Edge{{To: "Alpha", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "a_test.go"}}}
	ApplyCallEdgeBonus(ranked, edges, 2, false)
	if ranked[0].Components[ComponentCallers] != 0 {
		t.Fatalf("test caller was scored: %#v", ranked[0])
	}
	ApplyCallEdgeBonus(ranked, edges, 2, true)
	if ranked[0].Components[ComponentCallers] == 0 {
		t.Fatalf("test caller missing: %#v", ranked[0])
	}
}

func TestApplyCallEdgeBonusRejectsLexicalEvidence(t *testing.T) {
	t.Parallel()
	ranked := []RankedFile{{Path: "a.go", ImportedBy: 2, Symbols: []analyze.Symbol{{Name: "Alpha", Exported: true}}, Components: map[string]int{}}}
	ApplyCallEdgeBonus(ranked, []analyze.Edge{{To: "Alpha", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "other.go"}}}, 2, true)
	if ranked[0].Components[ComponentCallers] != 0 {
		t.Fatalf("lexical caller was scored: %#v", ranked[0])
	}
}
