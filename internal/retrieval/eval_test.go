package retrieval

import (
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestReferenceGraphEvaluationCorpusContract(t *testing.T) {
	t.Parallel()

	cases, err := LoadEvalCases(filepath.Join("..", "..", "testdata", "retrieval", "reference-graph-v1", "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 50 {
		t.Fatalf("cases = %d, want 50", len(cases))
	}
	lexical, nonlexical := 0, 0
	for _, c := range cases {
		if c.TokenBudget != 1024 {
			t.Fatalf("case %s budget = %d", c.ID, c.TokenBudget)
		}
		switch c.Classification {
		case "lexical":
			lexical++
		case "nonlexical":
			nonlexical++
		}
	}
	if lexical != 25 || nonlexical != 25 {
		t.Fatalf("lexical = %d, nonlexical = %d", lexical, nonlexical)
	}
}

func TestEvaluateUsesTaskPolicyAndReportsInclusion(t *testing.T) {
	snap := analyze.Snapshot{Root: t.TempDir(), Files: []analyze.File{{Path: "internal/cache/cache.go", Language: "go"}}, Symbols: []analyze.Symbol{{Name: "LoadValidated", Location: analyze.Location{Path: "internal/cache/cache.go", Line: 1}}}, Captured: map[string][]byte{"internal/cache/cache.go": []byte("package cache\nfunc LoadValidated() {}\n")}}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: "snapshot-1"}
	cases := []EvalCase{{Schema: EvalCaseSchema, ID: "cache", SnapshotID: "snapshot-1", Request: "LoadValidated cache", TokenBudget: 4096, ExpectedPaths: []string{"internal/cache/cache.go"}, ExpectedSymbols: []string{"LoadValidated"}, Provenance: "observed test", Classification: "lexical"}}
	report, err := Evaluate(snap, cases, StructuralLexicalPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Cases[0].PathInclusion["internal/cache/cache.go"] || !report.Cases[0].SymbolInclusion["LoadValidated"] {
		t.Fatalf("result=%+v", report.Cases[0])
	}
	if report.Aggregate.PromotionEligible {
		t.Fatal("one case must not satisfy promotion minimum")
	}
}

func TestEvaluateRejectsSnapshotMismatch(t *testing.T) {
	snap := analyze.Snapshot{}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: "one"}
	_, err := Evaluate(snap, []EvalCase{{Schema: EvalCaseSchema, ID: "x", SnapshotID: "two", Request: "x", TokenBudget: 10, ExpectedPaths: []string{"x"}, Provenance: "observed", Classification: "nonlexical"}}, StructuralLexicalPolicy)
	if err == nil {
		t.Fatal("expected snapshot mismatch")
	}
}

func TestEvaluateSupportsReferenceGraphPolicy(t *testing.T) {
	snap := analyze.Snapshot{
		Files:    []analyze.File{{Path: "owner.ts", Language: "typescript"}, {Path: "use.ts", Language: "typescript"}},
		Symbols:  []analyze.Symbol{{Name: "Owner", Location: analyze.Location{Path: "owner.ts", Line: 1}}},
		Edges:    []analyze.Edge{{From: "use.ts", To: "owner.ts", Kind: "references", Symbol: "Owner", Confidence: analyze.ConfidenceSyntactic}},
		Captured: map[string][]byte{"owner.ts": []byte("export class Owner {}\n"), "use.ts": []byte("new Owner()\n")},
	}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: "graph-snapshot"}
	cases := []EvalCase{{Schema: EvalCaseSchema, ID: "graph", SnapshotID: "graph-snapshot", Request: "important owner", TokenBudget: 4096, ExpectedPaths: []string{"owner.ts"}, Provenance: "observed test", Classification: "nonlexical"}}
	report, err := Evaluate(snap, cases, StructuralReferenceGraphPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if report.PolicyID != StructuralReferenceGraphPolicy || report.Graph == nil || report.Graph.Edges != 1 || !report.Cases[0].PathInclusion["owner.ts"] {
		t.Fatalf("report = %#v", report)
	}
}

func TestEvaluateRejectsUnknownPolicy(t *testing.T) {
	snap := analyze.Snapshot{}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: "one"}
	_, err := Evaluate(snap, nil, "unknown/v1")
	if err == nil {
		t.Fatal("expected unsupported policy error")
	}
}
