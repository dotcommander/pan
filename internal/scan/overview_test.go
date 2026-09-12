package scan

import (
	"reflect"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestOverviewCountsAndSorting(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{
		Files: []analyze.File{
			{Path: "cmd/app/main.go", Language: languageGo},
			{Path: "internal/service/service.go", Language: languageGo},
			{Path: "internal/service/service_test.go", Language: languageGo},
			{Path: "gen/code_gen.go", Language: languageGo, Generated: true},
			{Path: "README.md", Language: "markdown"},
			{Path: "notes.txt", Language: "unknown"},
			{Path: "docs/guide.md", Language: "markdown"},
		},
		Symbols: []analyze.Symbol{
			{Location: analyze.Location{Path: "cmd/app/main.go"}},
			{Location: analyze.Location{Path: "cmd/app/main.go"}},
			{Location: analyze.Location{Path: "internal/service/service.go"}},
		},
		Edges: []analyze.Edge{{Kind: edgeKindImports}, {Kind: "calls"}},
	}
	report := Overview(snap)
	if report.Files != 7 || report.Symbols != 3 || report.Edges != 2 {
		t.Fatalf("counts = %d/%d/%d", report.Files, report.Symbols, report.Edges)
	}
	if report.GeneratedFiles != 1 || report.TestFiles != 1 {
		t.Fatalf("generated=%d test=%d, want 1/1", report.GeneratedFiles, report.TestFiles)
	}
	wantLangs := []LanguageCount{
		{Language: languageGo, Files: 4},
		{Language: "markdown", Files: 2},
		{Language: "unknown", Files: 1},
	}
	if !reflect.DeepEqual(report.Languages, wantLangs) {
		t.Fatalf("languages = %+v, want %+v", report.Languages, wantLangs)
	}
	wantPkgs := []string{"cmd/app", "internal/service"}
	if !reflect.DeepEqual(report.GoPackages, wantPkgs) {
		t.Fatalf("go_packages = %v, want %v", report.GoPackages, wantPkgs)
	}
	if report.Instructions == nil {
		t.Fatal("instructions must render as an empty list, not null")
	}
}
