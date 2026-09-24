package retrieval

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestTaskSkipsOversizedFirstCandidateToCoverAnotherGoalTerm(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Root: t.TempDir()}
	ranked := []ranking.RankedFile{
		{Path: "alpha.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{{Name: "Alpha", Doc: strings.Repeat("alpha documentation ", 1000), Location: analyze.Location{Path: "alpha.go", Line: 1}}}},
		{Path: "beta.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{{Name: "Beta", Location: analyze.Location{Path: "beta.go", Line: 1}}}},
	}
	report, err := Task(snap, ranked, "alpha beta", TaskOptions{Tokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Path != "beta.go" {
		t.Fatalf("feasible distinct term lost after oversized first: %+v", report)
	}
	if report.Budget.UsedTokens != taskTokens(report) || report.Budget.UsedTokens > report.Budget.MaxTokens {
		t.Fatalf("untruthful encoded budget: %+v", report.Budget)
	}
	if !taskTruncated(report, "targets", "target or token budget") || !taskTruncated(report, "goal_terms", "target or token budget") {
		t.Fatalf("omitted target or term not disclosed: %+v", report.Truncations)
	}
}

func TestTaskDropsOnlySourceWhenWholeTargetFits(t *testing.T) {
	t.Parallel()
	source := strings.Repeat(strings.Repeat("x", 120)+"\n", 60)
	snap := analyze.Snapshot{Root: t.TempDir(), Captured: map[string][]byte{"alpha.go": []byte(source)}}
	ranked := []ranking.RankedFile{{Path: "alpha.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{{Name: "Alpha", Location: analyze.Location{Path: "alpha.go", Line: 1}, EndLine: 60}}}}
	report, err := Task(snap, ranked, "alpha", TaskOptions{Tokens: 800})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || len(report.Targets[0].Source) != 0 {
		t.Fatalf("source-free complete target missing: %+v", report)
	}
	if !taskTruncated(report, "targets[alpha.go].source", "token budget") || report.Budget.UsedTokens != taskTokens(report) || report.Budget.UsedTokens > report.Budget.MaxTokens {
		t.Fatalf("source omission or budget not truthful: %+v", report)
	}
}

func TestTaskSelectionKeepsSingleTermRankAndSixTargetCap(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Root: t.TempDir()}
	var ranked []ranking.RankedFile
	for _, path := range []string{"omega8.go", "omega5.go", "omega7.go", "omega2.go", "omega1.go", "omega3.go", "omega6.go", "omega4.go"} {
		ranked = append(ranked, ranking.RankedFile{Path: path, Language: analyze.LanguageGo})
	}
	first, err := Task(snap, ranked, "omega", TaskOptions{Tokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(ranked)
	second, err := Task(snap, ranked, "omega", TaskOptions{Tokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.Targets) != taskTargetLimit || !taskTruncated(first, "targets", "target or token budget") {
		t.Fatalf("single-term ordering or cap changed: first=%+v second=%+v", first, second)
	}
	for i, target := range first.Targets {
		if target.Path != "omega"+string(rune('1'+i))+".go" {
			t.Fatalf("target[%d] = %s, want stable rank/path order", i, target.Path)
		}
	}
}

func TestTaskSixthTargetDoesNotReserveUnusedWorkCap(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Root: t.TempDir()}
	var ranked []ranking.RankedFile
	for i := range 100 {
		ranked = append(ranked, ranking.RankedFile{Path: fmt.Sprintf("omega%03d.go", i), Language: analyze.LanguageGo})
	}
	large, err := Task(snap, ranked, "omega", TaskOptions{Tokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(large.Targets) != taskTargetLimit || taskTruncated(large, "candidate_work", "selection work cap") {
		t.Fatalf("large packet did not fill before the work cap: %+v", large)
	}
	exact, err := Task(snap, ranked, "omega", TaskOptions{Tokens: large.Budget.UsedTokens})
	if err != nil {
		t.Fatal(err)
	}
	if len(exact.Targets) != taskTargetLimit || exact.Budget.UsedTokens > exact.Budget.MaxTokens || exact.Budget.UsedTokens != taskTokens(exact) {
		t.Fatalf("unused work-cap reserve rejected a feasible target: %+v", exact)
	}
}

func TestTaskFallbackCountsDistinctFiles(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Root: t.TempDir()}
	ranked := []ranking.RankedFile{{Path: "one.go"}, {Path: "one.go"}, {Path: "two.go"}}
	report, err := Task(snap, ranked, "unmatched", TaskOptions{Tokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 2 || report.Targets[0].Path != "one.go" || report.Targets[1].Path != "two.go" || taskTruncated(report, "targets", "target or token budget") {
		t.Fatalf("duplicate path inflated candidate count: %+v", report)
	}
}

func TestTaskCallRelationsDoNotFanOutCollidingNames(t *testing.T) {
	t.Parallel()
	a := analyze.Symbol{Name: "Run", Location: analyze.Location{Path: "a.go", Line: 2}}
	b := analyze.Symbol{Name: "Run", Location: analyze.Location{Path: "b.go", Line: 2}}
	snap := analyze.Snapshot{Symbols: []analyze.Symbol{a, b}, Edges: []analyze.Edge{
		{From: "Use", To: "Run", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Target: &a.Location, Location: analyze.Location{Path: "use.go", Line: 3}},
		{From: "Maybe", To: "Run", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "other.go", Line: 3}},
	}}
	if got := lexicalCallerFiles(snap, ranking.RankedFile{Path: "a.go", Symbols: []analyze.Symbol{a}}); !reflect.DeepEqual(got, []string{"use.go"}) {
		t.Fatalf("a.go callers = %v", got)
	}
	if got := lexicalCallerFiles(snap, ranking.RankedFile{Path: "b.go", Symbols: []analyze.Symbol{b}}); len(got) != 0 {
		t.Fatalf("b.go inherited unrelated callers: %v", got)
	}
}

func taskTruncated(report TaskReport, field, reason string) bool {
	for _, item := range report.Truncations {
		if item.Field == field && item.Reason == reason {
			return true
		}
	}
	return false
}
