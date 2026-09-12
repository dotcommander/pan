package ranking

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestRankKeepsFilesWithoutSymbolEvidence(t *testing.T) {
	t.Parallel()
	snapshot := analyze.Snapshot{Files: []analyze.File{{Path: "assets/embed.go", Language: "go"}}}
	ranked := Rank(snapshot, "", Options{})
	if len(ranked) != 1 || ranked[0].Path != "assets/embed.go" {
		t.Fatalf("ranked = %#v", ranked)
	}
}

func TestRankDemotesTestsOnlyWhenNotIncluded(t *testing.T) {
	t.Parallel()
	snapshot := analyze.Snapshot{Files: []analyze.File{
		{Path: "service.go", Language: "go"},
		{Path: "service_test.go", Language: "go"},
	}}
	defaultRank := Rank(snapshot, "", Options{})
	fullRank := Rank(snapshot, "", Options{IncludeTests: true})
	if componentFor(defaultRank, "service_test.go", ComponentTestDemote) != -15 {
		t.Fatalf("default test component = %v", defaultRank)
	}
	if componentFor(fullRank, "service_test.go", ComponentTestDemote) != 0 {
		t.Fatalf("included test component = %v", fullRank)
	}
}

func TestReferenceGraphPolicyIsOptionalAndDeterministic(t *testing.T) {
	t.Parallel()
	snapshot := analyze.Snapshot{
		Files:   []analyze.File{{Path: "a.ts", Language: "typescript"}, {Path: "b.ts", Language: "typescript"}, {Path: "c.ts", Language: "typescript"}},
		Symbols: []analyze.Symbol{{Name: "Hub", Location: analyze.Location{Path: "a.ts", Line: 1}}},
		Edges: []analyze.Edge{
			{From: "b.ts", To: "a.ts", Kind: "references", Symbol: "Hub", Confidence: analyze.ConfidenceSyntactic},
			{From: "c.ts", To: "a.ts", Kind: "references", Symbol: "Hub", Confidence: analyze.ConfidenceSyntactic},
		},
	}
	baseline := Rank(snapshot, "", Options{})
	if componentFor(baseline, "a.ts", ComponentReferenceGraph) != 0 {
		t.Fatalf("baseline unexpectedly has graph component: %#v", baseline)
	}
	first, stats := RankWithStats(snapshot, "", Options{ReferenceGraph: true})
	second, secondStats := RankWithStats(snapshot, "", Options{ReferenceGraph: true})
	if first[0].Path != "a.ts" || componentFor(first, "a.ts", ComponentReferenceGraph) == 0 {
		t.Fatalf("graph rank = %#v, want a.ts boosted first", first)
	}
	if stats != secondStats || len(first) != len(second) {
		t.Fatalf("stats first=%+v second=%+v", stats, secondStats)
	}
	for i := range first {
		if first[i].Path != second[i].Path || first[i].Score != second[i].Score {
			t.Fatalf("non-deterministic ranks: first=%#v second=%#v", first, second)
		}
	}
}

func componentFor(ranked []RankedFile, file, component string) int {
	for _, item := range ranked {
		if item.Path == file {
			return item.Components[component]
		}
	}
	return 0
}
