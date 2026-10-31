package retrieval

import (
	"reflect"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestTaskRelationshipIndexPreservesCallAndImporterEvidence(t *testing.T) {
	t.Parallel()
	a := analyze.Symbol{Name: "Run", Location: analyze.Location{Path: "pkg/alpha.go", Line: 2}}
	b := analyze.Symbol{Name: "Run", Location: analyze.Location{Path: "other/run.go", Line: 2}}
	unique := analyze.Symbol{Name: "Start", Location: analyze.Location{Path: "pkg/alpha.go", Line: 3}}
	snap := analyze.Snapshot{Root: t.TempDir(), Symbols: []analyze.Symbol{a, b, unique}, Edges: []analyze.Edge{
		{To: "Run", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Target: &a.Location, Location: analyze.Location{Path: "pkg/typed.go", Line: 4}},
		{To: "Run", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Target: &b.Location, Location: analyze.Location{Path: "pkg/wrong.go", Line: 4}},
		{To: "Run", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "pkg/ambiguous.go", Line: 5}},
		{To: "Start", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "pkg/lexical.go", Line: 6}},
		{To: "Start", Kind: "calls", Confidence: analyze.ConfidenceLexical, Location: analyze.Location{Path: "pkg/alpha.go", Line: 7}},
	}}
	ranked := []ranking.RankedFile{
		{Path: "pkg/alpha.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{a, unique}},
		{Path: "pkg/importer.go", Language: analyze.LanguageGo, InternalImports: []string{"pkg", "pkg"}},
		{Path: "pkg/typed.go", Language: analyze.LanguageGo},
		{Path: "pkg/lexical.go", Language: analyze.LanguageGo},
		{Path: "other/run.go", Language: analyze.LanguageGo, Symbols: []analyze.Symbol{b}},
	}
	index := newTaskRelationships(snap, ranked)
	wantImporters := []string{"pkg/importer.go"}
	wantCallers := []string{"pkg/lexical.go", "pkg/typed.go"}
	if got := index.importersOf(ranked[0]); !reflect.DeepEqual(got, wantImporters) {
		t.Fatalf("indexed importers = %v, want %v", got, wantImporters)
	}
	if got := index.callersOf(ranked[0]); !reflect.DeepEqual(got, wantCallers) {
		t.Fatalf("indexed callers = %v, want %v", got, wantCallers)
	}
	// One request-local index serves candidate expansion and target assembly.
	candidates := taskCandidatesWithRelationships(ranked, "alpha", index)
	if len(candidates) == 0 || candidates[0].file.Path != "pkg/alpha.go" {
		t.Fatalf("missing goal owner: %+v", candidates)
	}
	target, _ := buildTaskTarget(snap, ranked, candidates[0], nil, index)
	if !reflect.DeepEqual(target.Consumers, wantImporters) || !reflect.DeepEqual(target.Relations, []TaskRelationship{
		{Kind: "caller", Path: "pkg/lexical.go", Provenance: analyze.ConfidenceLexical},
		{Kind: "caller", Path: "pkg/typed.go", Provenance: analyze.ConfidenceLexical},
		{Kind: "consumer", Path: "pkg/importer.go", Provenance: analyze.ConfidenceSyntactic},
	}) {
		t.Fatalf("target relationship evidence changed: %+v", target)
	}
	report, err := Task(snap, ranked, "alpha", TaskOptions{Tokens: 4096, Consumed: []string{"pkg/alpha.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) == 0 || report.Targets[0].Path != target.Path || !reflect.DeepEqual(report.Targets[0].Relations, target.Relations) || !report.Targets[0].Consumed || len(report.Targets[0].Source) != 0 {
		t.Fatalf("task changed indexed evidence or consumed source handling: %+v", report)
	}
	if report.Budget.UsedTokens != taskTokens(report) || report.Budget.UsedTokens > report.Budget.MaxTokens {
		t.Fatalf("incorrect packet budget: %+v", report.Budget)
	}
}
