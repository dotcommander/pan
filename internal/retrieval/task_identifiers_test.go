package retrieval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestTaskPrioritizesNamedSymbolsWithinMixedGoal(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Root: t.TempDir(), Captured: make(map[string][]byte)}
	noise := ranking.RankedFile{Path: "a/brief_symbol.go", Language: analyze.LanguageGo, Score: 1000}
	for i := range 20 {
		noise.Symbols = append(noise.Symbols, analyze.Symbol{
			Name: fmt.Sprintf("BriefSymbolEcho%d", i), Kind: "function", Exported: true,
			Doc: "brief calls commands symbol handle", Location: analyze.Location{Path: noise.Path, Line: i + 1},
		})
	}
	ranked := []ranking.RankedFile{noise}
	for _, fixture := range []struct{ path, name string }{
		{"z/brief_next.go", "briefCallCommands"},
		{"z/find.go", "SymbolHandle"},
	} {
		file := ranking.RankedFile{Path: fixture.path, Language: analyze.LanguageGo}
		source := "package sample\n"
		for i, name := range []string{"Alpha", "Beta", "Gamma", fixture.name} {
			file.Symbols = append(file.Symbols, analyze.Symbol{
				Name: name, Kind: "function", Exported: name != "briefCallCommands",
				Location: analyze.Location{Path: fixture.path, Line: i + 2}, EndLine: i + 2,
			})
			source += "func " + name + "() {}\n"
		}
		snap.Captured[fixture.path] = []byte(source)
		ranked = append(ranked, file)
	}
	for _, file := range ranked {
		snap.Files = append(snap.Files, analyze.File{Path: file.Path, Language: file.Language})
		snap.Symbols = append(snap.Symbols, file.Symbols...)
	}

	for _, budget := range []int{1024, 4096} {
		t.Run(fmt.Sprintf("budget_%d", budget), func(t *testing.T) {
			t.Parallel()
			report, err := Task(snap, ranked, "briefCallCommands callers SymbolHandle", TaskOptions{Tokens: budget})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Targets) < 2 {
				t.Fatalf("named owners lost to broad matches: %+v", report)
			}
			for i, want := range []struct{ path, name string }{
				{"z/brief_next.go", "briefCallCommands"}, {"z/find.go", "SymbolHandle"},
			} {
				target := report.Targets[i]
				if target.Path != want.path || len(target.Symbols) == 0 || target.Symbols[0].Name != want.name {
					t.Fatalf("target[%d] lost named declaration %s in %s: %+v", i, want.name, want.path, target)
				}
				if budget == 4096 && len(target.Source) == 0 {
					t.Fatalf("captured named source unexpectedly omitted: %+v", target)
				}
				if len(target.Source) > 0 && target.Source[0].Symbol != want.name {
					t.Fatalf("source is unrelated to requested declaration: %+v", target.Source)
				}
			}
			if report.Budget.UsedTokens != taskTokens(report) || report.Budget.UsedTokens > budget {
				t.Fatalf("packet exceeds encoded budget: %+v", report.Budget)
			}
		})
	}
}

func TestTaskDisclosesOmittedNamedIdentifierDespitePrefixEcho(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Root: t.TempDir()}
	ranked := []ranking.RankedFile{
		{Path: "owner.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{{
			Name: "ParseConfig", Kind: "function", Doc: strings.Repeat("parse config documentation ", 1000),
			Location: analyze.Location{Path: "owner.go", Line: 1},
		}}},
		{Path: "echo.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{{
			Name: "Parser", Kind: "function", Doc: "parse config", Location: analyze.Location{Path: "echo.go", Line: 1},
		}}},
	}
	report, err := Task(snap, ranked, "ParseConfig", TaskOptions{Tokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Path != "echo.go" {
		t.Fatalf("feasible broad evidence should survive oversized owner: %+v", report)
	}
	if !taskTruncated(report, "goal_terms", "target or token budget") {
		t.Fatalf("prefix echo incorrectly covered omitted named declaration: %+v", report.Truncations)
	}
}
