package retrieval

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestGoalTermExpansionMatchesCompoundSymbols(t *testing.T) {
	t.Parallel()
	// "parse config" as a natural-language goal must surface the definer of
	// ParseConfig through sub-token expansion, not substring luck.
	snap := analyze.Snapshot{
		Root:    t.TempDir(),
		Files:   []analyze.File{{Path: "config.go", Language: "go"}, {Path: "noise.go", Language: "go"}},
		Symbols: []analyze.Symbol{{Name: "ParseConfig", Location: analyze.Location{Path: "config.go", Line: 1}}},
		Captured: map[string][]byte{
			"config.go": []byte("package main\nfunc ParseConfig() {}\n"),
			"noise.go":  []byte("package main\nfunc Unrelated() {}\n"),
		},
	}
	ranked, _ := ranking.RankWithStats(snap, "", ranking.Options{Intent: "parse config"})
	report, err := Task(snap, ranked, "parse config", TaskOptions{Tokens: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if !reportCoverPath(report, "config.go") {
		t.Fatalf("expected config.go target, got %+v", report.Targets)
	}
}

func TestSymbolGoalPrefersDefinersOverPathEchoes(t *testing.T) {
	t.Parallel()
	// A bare-symbol goal "ParseConfig" must rank the definer above a file
	// whose path merely echoes the term.
	snap := analyze.Snapshot{
		Root: t.TempDir(),
		Files: []analyze.File{
			{Path: "a/parseconfig_notes.go", Language: "go"},
			{Path: "z/service.go", Language: "go"},
		},
		Symbols: []analyze.Symbol{
			{Name: "ParseConfig", Location: analyze.Location{Path: "z/service.go", Line: 1}},
		},
		Captured: map[string][]byte{
			"a/parseconfig_notes.go": []byte("package a\n// notes about parse config\n"),
			"z/service.go":           []byte("package z\nfunc ParseConfig() {}\n"),
		},
	}
	if !isSymbolGoal("ParseConfig") {
		t.Fatal("ParseConfig should be detected as a symbol goal")
	}
	if isSymbolGoal("parse the config please") {
		t.Fatal("natural-language goal misdetected as symbol goal")
	}
	ranked, _ := ranking.RankWithStats(snap, "", ranking.Options{Intent: "ParseConfig"})
	report, err := Task(snap, ranked, "ParseConfig", TaskOptions{Tokens: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) == 0 || report.Targets[0].Path != "z/service.go" {
		t.Fatalf("expected definer z/service.go first, got %+v", report.Targets)
	}
}

func TestFieldEvidenceRejectsMidWordSubstrings(t *testing.T) {
	t.Parallel()
	// 'utho' never matches Author: mid-word substrings are not evidence.
	file := rankedFileFor(t, "service.go", "Author")
	if _, _, score := fieldEvidence(file, []string{"utho"}, false); score != 0 {
		t.Fatalf("mid-word substring matched with score=%d", score)
	}
	// 'author' matches compound Authorizer at partial (prefix) strength.
	file = rankedFileFor(t, "service.go", "Authorizer")
	evidence, _, score := fieldEvidence(file, []string{"author"}, false)
	exactFile := rankedFileFor(t, "service.go", "Author")
	_, _, exactScore := fieldEvidence(exactFile, []string{"author"}, false)
	if score <= 0 || len(evidence) == 0 {
		t.Fatal("sub-token prefix should match Authorizer")
	}
	if score >= exactScore {
		t.Fatalf("prefix match (%d) should score below exact match (%d)", score, exactScore)
	}
}

func rankedFileFor(t *testing.T, path string, symbols ...string) *ranking.RankedFile {
	t.Helper()
	rf := &ranking.RankedFile{Path: path}
	for _, name := range symbols {
		rf.Symbols = append(rf.Symbols, analyze.Symbol{Name: name, Location: analyze.Location{Path: path, Line: 1}})
	}
	return rf
}

func reportCoverPath(report TaskReport, want string) bool {
	for _, target := range report.Targets {
		if target.Path == want {
			return true
		}
		for _, relation := range target.Relations {
			if relation.Path == want {
				return true
			}
		}
	}
	return false
}
