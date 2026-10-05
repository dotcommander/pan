package retrieval

import (
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func efficiencySnapshot(t *testing.T) analyze.Snapshot {
	t.Helper()
	pad := strings.Repeat("x", 8192) // 8192 chars -> 2048 read tokens
	snap := analyze.Snapshot{
		Root: t.TempDir(),
		Files: []analyze.File{
			{Path: "big_noise.go", Language: "go"},
			{Path: "tiny/config.go", Language: "go"},
		},
		Symbols: []analyze.Symbol{{Name: "ParseConfig", Location: analyze.Location{Path: "tiny/config.go", Line: 1}}},
		Captured: map[string][]byte{
			// baseline keywords appear in both files; the big file wins the
			// distinct-term tie only on content but loses nothing — grep+read
			// must still read it before the relevant file when ranking puts
			// it first by distinct terms.
			"big_noise.go":   []byte("package big\n// parse config " + pad + "\nfunc Big() {}\n"),
			"tiny/config.go": []byte("package config\nfunc ParseConfig() {}\n"),
		},
	}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: "eff-snapshot"}
	return snap
}

func TestEvaluateEfficiencyComparesPacketAgainstBaseline(t *testing.T) {
	t.Parallel()
	snap := efficiencySnapshot(t)
	cases := []EvalCase{{Schema: EvalCaseSchema, ID: "parse", SnapshotID: "eff-snapshot", Request: "parse config", TokenBudget: 1024, ExpectedPaths: []string{"tiny/config.go"}, ExpectedSymbols: []string{"ParseConfig"}, Provenance: "observed test", Classification: "lexical", Category: CategorySemantic}}
	report, err := Evaluate(snap, cases, StructuralLexicalPolicy, EvalOptions{Efficiency: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Efficiency == nil {
		t.Fatal("efficiency summary missing")
	}
	if report.Cases[0].NDCGAt10 <= 0 {
		t.Fatalf("expected positive NDCG for covered case: %+v", report.Cases[0])
	}
	var packet, baseline *MethodEfficiency
	for i := range report.Efficiency.Methods {
		switch report.Efficiency.Methods[i].Name {
		case efficiencyMethodPacket:
			packet = &report.Efficiency.Methods[i]
		case efficiencyMethodBaseline:
			baseline = &report.Efficiency.Methods[i]
		}
	}
	if packet == nil || baseline == nil {
		t.Fatalf("methods = %+v", report.Efficiency.Methods)
	}
	if packet.ExpectedTokens >= baseline.ExpectedTokens {
		t.Fatalf("packet (%.0f tokens) should beat baseline (%.0f tokens) on this fixture", packet.ExpectedTokens, baseline.ExpectedTokens)
	}
	// The baseline reads big_noise.go (2000+ tokens) before the relevant file.
	if baseline.ExpectedTokens < 2000 {
		t.Fatalf("baseline cost implausibly small: %.0f", baseline.ExpectedTokens)
	}
	for _, curve := range [][]BudgetRecall{packet.RecallAtBudget, baseline.RecallAtBudget} {
		if !slices.IsSortedFunc(curve, func(a, b BudgetRecall) int { return a.Tokens - b.Tokens }) {
			t.Fatalf("recall budgets not ascending: %+v", curve)
		}
	}
}

func TestEvaluateCategoryValidationAndCounts(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Root: t.TempDir(), Files: []analyze.File{{Path: "a.go", Language: "go"}}, Captured: map[string][]byte{"a.go": []byte("package a\n")}}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: "s1"}
	bad := EvalCase{Schema: EvalCaseSchema, ID: "x", SnapshotID: "s1", Request: "x", TokenBudget: 512, ExpectedPaths: []string{"a.go"}, Provenance: "observed", Classification: "lexical", Category: "exotic"}
	if _, err := Evaluate(snap, []EvalCase{bad}, StructuralLexicalPolicy); err == nil {
		t.Fatal("expected category validation error")
	}
	good := EvalCase{Schema: EvalCaseSchema, ID: "x", SnapshotID: "s1", Request: "a", TokenBudget: 512, ExpectedPaths: []string{"a.go"}, Provenance: "observed", Classification: "lexical", Category: CategorySymbol}
	report, err := Evaluate(snap, []EvalCase{good}, StructuralLexicalPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if report.Aggregate.CategoryCounts[CategorySymbol] != 1 || report.Cases[0].Category != CategorySymbol {
		t.Fatalf("category tracking = %+v", report.Aggregate)
	}
}

func TestCaseNDCGDistinguishesOrderingsMRRCannot(t *testing.T) {
	t.Parallel()
	c := EvalCase{ExpectedPaths: []string{"want_a.go", "want_b.go"}}
	// Same RR-relevant first hit, different second-target order: NDCG must
	// reward the ordering that places both relevant files higher.
	good := TaskReport{Targets: []TaskTarget{{Path: "want_a.go"}, {Path: "want_b.go"}, {Path: "noise.go"}}}
	bad := TaskReport{Targets: []TaskTarget{{Path: "want_a.go"}, {Path: "noise.go"}, {Path: "want_b.go"}}}
	if caseNDCG(c, good) <= caseNDCG(c, bad) {
		t.Fatalf("NDCG must distinguish orderings: good=%f bad=%f", caseNDCG(c, good), caseNDCG(c, bad))
	}
	if caseNDCG(c, TaskReport{Targets: []TaskTarget{{Path: "noise.go"}}}) != 0 {
		t.Fatal("uncovered case must score zero")
	}
}
