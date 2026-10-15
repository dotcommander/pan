package retrieval

import (
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestTaskSentenceCapitalizationIsNotExplicitNaming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goal string
		want []string
	}{
		{"Locate serve request timeout", nil},
		{"Inspect request timeout", nil},
		{"Locate", []string{"locate"}},
		{"Read callers", nil},
		{"Inspect `Read` callers", []string{"read"}},
		{"Locate SymbolHandle callers", []string{"symbolhandle"}},
		{"Inspect ns.read and _serve", []string{"_serve", "ns.read"}},
	} {
		t.Run(tc.goal, func(t *testing.T) {
			t.Parallel()
			if got := taskGoalIdentifiers(tc.goal); !slices.Equal(got, tc.want) {
				t.Fatalf("identifiers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTaskNaturalGoalKeepsRelevantOwnerAndExcerpt(t *testing.T) {
	t.Parallel()
	for _, goal := range []string{"Locate serve request timeout", "serve request timeout"} {
		t.Run(goal, func(t *testing.T) {
			t.Parallel()
			path := "server.go"
			snap := analyze.Snapshot{
				Root:  t.TempDir(),
				Files: []analyze.File{{Path: path, Language: "go"}, {Path: "noise.go", Language: "go"}},
				Captured: map[string][]byte{
					path:       []byte("package sample\nfunc Alpha() {}\nfunc Beta() {}\nfunc Error() {}\nfunc RunServeWithTimeout() {}\n"),
					"noise.go": []byte("package sample\nfunc locate() {}\n"),
				},
			}
			owner := ranking.RankedFile{Path: path, Language: "go"}
			for i, name := range []string{"Alpha", "Beta", "Error", "RunServeWithTimeout"} {
				owner.Symbols = append(owner.Symbols, analyze.Symbol{
					Name: name, Kind: "function", Exported: true,
					Location: analyze.Location{Path: path, Line: i + 2}, EndLine: i + 2,
				})
			}
			noise := ranking.RankedFile{Path: "noise.go", Language: "go", Symbols: []analyze.Symbol{{
				Name: "locate", Kind: "function", Location: analyze.Location{Path: "noise.go", Line: 2}, EndLine: 2,
			}}}
			ranked := []ranking.RankedFile{noise, owner}
			for _, f := range ranked {
				snap.Symbols = append(snap.Symbols, f.Symbols...)
			}
			report, err := Task(snap, ranked, goal, TaskOptions{Tokens: 4096})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Targets) == 0 || report.Targets[0].Path != path {
				t.Fatalf("sentence word displaced relevant owner: %+v", report.Targets)
			}
			target := report.Targets[0]
			if len(target.Symbols) == 0 || target.Symbols[0].Name != "RunServeWithTimeout" ||
				len(target.Source) != 1 || target.Source[0].Symbol != "RunServeWithTimeout" ||
				len(target.Source[0].Lines) != 1 || target.Source[0].Lines[0].Number != 5 ||
				target.Source[0].Lines[0].Text != "func RunServeWithTimeout() {}" {
				t.Fatalf("unrelated excerpt despite goal-relevant declaration: %+v", target)
			}
			if len(report.ReadNext) == 0 || report.ReadNext[0].Path != path || report.ReadNext[0].Start != 5 {
				t.Fatalf("read hint lost goal-relevant declaration: %+v", report.ReadNext)
			}
			if report.Budget.UsedTokens != taskTokens(report) || report.Budget.UsedTokens > 4096 {
				t.Fatalf("invalid charged budget: %+v", report.Budget)
			}
			if owner.Symbols[0].Name != "Alpha" {
				t.Fatal("selection mutated the caller's symbol order")
			}
		})
	}
}
