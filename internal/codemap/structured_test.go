package codemap

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestBuildStructuredPreservesSchemaTotalsAndSelection(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{
		Root:  "/repo",
		Files: []analyze.File{{Path: "cmd/main.go", Language: "go"}, {Path: "internal/auth/token.go", Language: "go"}},
		Symbols: []analyze.Symbol{
			{Name: "Main", Kind: "function", Exported: true, Location: analyze.Location{Path: "cmd/main.go", Line: 3}},
			{Name: "Refresh", Kind: "function", Exported: true, Signature: "() error", Location: analyze.Location{Path: "internal/auth/token.go", Line: 12}},
		},
	}
	ranked := []ranking.RankedFile{
		{Path: "cmd/main.go", Language: "go", Score: 40, Components: map[string]int{"entry": 40}, Symbols: snap.Symbols[:1]},
		{Path: "internal/auth/token.go", Language: "go", Score: 20, Components: map[string]int{"symbols": 20}, Symbols: snap.Symbols[1:]},
	}

	out := BuildStructured(snap, ranked, Options{Tokens: 100, Intent: "refresh", Consumed: []string{"cmd/main.go"}, SymbolRefs: true})
	if out.SchemaVersion != 2 || out.Root != "/repo" {
		t.Fatalf("identity = %#v", out)
	}
	if out.Totals != (StructuredTotals{Files: 2, Symbols: 2}) {
		t.Fatalf("totals = %#v", out.Totals)
	}
	if out.Selection.TotalFiles != 2 || out.Selection.SelectedFiles != len(out.Files) || out.Selection.OmittedFiles != 0 {
		t.Fatalf("selection = %#v", out.Selection)
	}
	if len(out.Files) != out.Selection.SelectedFiles || len(out.Files) == 0 {
		t.Fatalf("selected files = %d, selection = %#v", len(out.Files), out.Selection)
	}
	file := out.Files[0]
	if file.Handle != "file:"+file.Path || file.ParseMethod != "go_ast" || file.CapabilityTier != "syntax" {
		t.Fatalf("file = %#v", file)
	}
	if len(file.Symbols) != 1 || file.Symbols[0].Handle != "symbol:"+file.Path+"::Main#function@3" {
		t.Fatalf("symbols = %#v", file.Symbols)
	}
	if out.Coverage.FilesScanned != 2 || out.Coverage.FilesParsed != 2 || !out.Coverage.TreeSitterEnabled || out.Coverage.CtagsEnabled {
		t.Fatalf("coverage = %#v", out.Coverage)
	}
}
