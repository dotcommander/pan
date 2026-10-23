package ranking

import (
	"fmt"
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
	if err := ApplySymbolReferenceBonus(analyze.Snapshot{Captured: map[string][]byte{"model.ts": []byte("export class Widget {}"), "use.ts": []byte("new Widget()")}}, ranked); err != nil {
		t.Fatal(err)
	}
	if ranked[0].Path != "model.ts" || ranked[0].Components["symbol_refs"] == 0 {
		t.Fatalf("ranked = %#v", ranked)
	}
}

func TestApplyCallEdgeBonusFiltersTestEvidence(t *testing.T) {
	t.Parallel()
	ranked := []RankedFile{{Path: "a.go", ImportedBy: 2, Symbols: []analyze.Symbol{{Name: "Alpha", Exported: true, Location: analyze.Location{Path: "a.go", Line: 1}}}, Components: map[string]int{}}}
	edges := []analyze.Edge{{To: "Alpha", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "a_test.go"}, Target: &analyze.Location{Path: "a.go", Line: 1}}}
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
	ranked := []RankedFile{{Path: "a.go", ImportedBy: 2, Symbols: []analyze.Symbol{{Name: "Alpha", Exported: true, Location: analyze.Location{Path: "a.go", Line: 1}}}, Components: map[string]int{}}}
	ApplyCallEdgeBonus(ranked, []analyze.Edge{{To: "Alpha", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "other.go"}}}, 2, true)
	if ranked[0].Components[ComponentCallers] != 0 {
		t.Fatalf("lexical caller was scored: %#v", ranked[0])
	}
}

func TestApplyCallEdgeBonusIsIdempotentAndCapped(t *testing.T) {
	t.Parallel()
	symLoc := analyze.Location{Path: "a.go", Line: 1}
	ranked := []RankedFile{{
		Path: "a.go", ImportedBy: 2,
		Symbols:    []analyze.Symbol{{Name: "Alpha", Exported: true, Location: symLoc}},
		Components: map[string]int{ComponentCallers: 18},
		Score:      18,
	}}
	var edges []analyze.Edge
	for i := 0; i < 15; i++ {
		edges = append(edges, analyze.Edge{
			To: "Alpha", Kind: "calls", Confidence: analyze.ConfidenceConfirmed,
			Location: analyze.Location{Path: fmt.Sprintf("caller%d.go", i)},
			Target:   &symLoc,
		})
	}
	// First pass should promote from 18 to max 20, not 18 + 20 = 38
	ApplyCallEdgeBonus(ranked, edges, 2, true)
	if ranked[0].Components[ComponentCallers] != 20 || ranked[0].Score != 20 {
		t.Fatalf("expected capped at 20, got component=%d, score=%d", ranked[0].Components[ComponentCallers], ranked[0].Score)
	}
	// Second pass must be idempotent
	ApplyCallEdgeBonus(ranked, edges, 2, true)
	if ranked[0].Components[ComponentCallers] != 20 || ranked[0].Score != 20 {
		t.Fatalf("expected idempotent 20, got component=%d, score=%d", ranked[0].Components[ComponentCallers], ranked[0].Score)
	}
}
