package review

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/scan"
)

import "testing"

func TestApplyOptionsFiltersAndExplainsSelectedRows(t *testing.T) {
	t.Parallel()
	report := Report{ReadQueue: []ReadItem{
		{Rank: 1, Path: "cmd/main.go", Score: 30, Why: []string{"risk:security", "hygiene:dirty"}},
		{Rank: 2, Path: "internal/auth/token.go", Score: 20, Why: []string{"risk:security", "churn:2 commits"}},
		{Rank: 3, Path: "docs/guide.md", Score: 50, Why: []string{"risk:docs"}},
	}}
	got, err := ApplyOptions(report, Options{Include: []string{"internal/**"}, Focus: "token|security", WhyTop: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ReadQueue) != 1 || got.ReadQueue[0].Path != "internal/auth/token.go" || got.ReadQueue[0].Rank != 1 {
		t.Fatalf("queue = %#v", got.ReadQueue)
	}
	if got.ReadQueue[0].Lane != LaneKept || !ValidIdentity(got.ReadQueue[0].EvidenceID) {
		t.Fatalf("selected item = %#v", got.ReadQueue[0])
	}
	if len(got.Rationale) != 1 || got.Rationale[0].Path != got.ReadQueue[0].Path {
		t.Fatalf("rationale = %#v", got.Rationale)
	}
}

func TestApplyOptionsInventoryUsesCanonicalReviewLane(t *testing.T) {
	t.Parallel()
	report := Report{ReadQueue: []ReadItem{{Rank: 1, Path: "internal/store/state.go", Score: 50, Why: []string{"risk:data-integrity"}}}}
	got, err := ApplyOptions(report, Options{Inventory: "data-integrity"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ReadQueue) != 1 || got.ReadQueue[0].Path != "internal/store/state.go" {
		t.Fatalf("queue = %#v", got.ReadQueue)
	}
}

func TestApplyOptionsInventoryPortsSourceFallbackPredicates(t *testing.T) {
	t.Parallel()
	report := Report{ReadQueue: []ReadItem{
		{Rank: 1, Path: "internal/storage/cache.go", Score: 50},
		{Rank: 2, Path: "cmd/pan/main.go", Score: 40},
		{Rank: 3, Path: "internal/worker.go", Score: 30, Why: []string{"detector:error_context_dropped"}},
		{Rank: 4, Path: "internal/unrelated.go", Score: 20},
	}}
	for _, lane := range []string{"data-integrity", "error-handling"} {
		got, err := ApplyOptions(report, Options{Inventory: lane})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.ReadQueue) == 0 {
			t.Fatalf("%s inventory lost source fallback rows", lane)
		}
	}
}

func TestComposePromotesFixHistoryEvidence(t *testing.T) {
	report := Compose(Packets{Changes: scan.ChangesReport{GitAvailable: true, Days: 30, TopFiles: []scan.FileChurn{{Path: "internal/store/state.go", Commits: 3, FixTouches: 2}}}}, 0)
	if len(report.ReadQueue) != 1 || report.ReadQueue[0].Score != 7 {
		t.Fatalf("history queue = %#v", report.ReadQueue)
	}
	if !slices.Contains(report.ReadQueue[0].Why, "history:2 fix touches") {
		t.Fatalf("history reasons = %#v", report.ReadQueue[0].Why)
	}
}

func TestApplyOptionsWithSelectionRecordsPredicatePrecedence(t *testing.T) {
	t.Parallel()
	report := Report{ReadQueue: []ReadItem{{Rank: 1, Path: "cmd/outside.go", Score: 20, Why: []string{"risk:security"}}}}
	cases := []struct {
		name    string
		options Options
		reason  SelectionReason
	}{
		{
			name:    "include wins",
			options: Options{Include: []string{"internal/**"}, Exclude: []string{"cmd/**"}, Inventory: "data-integrity", Focus: "nomatch"},
			reason:  ReasonIncludeMismatch,
		},
		{
			name:    "exclude follows include",
			options: Options{Exclude: []string{"cmd/**"}, Inventory: "data-integrity", Focus: "nomatch"},
			reason:  ReasonExcludeMatch,
		},
		{
			name:    "inventory follows exclude",
			options: Options{Inventory: "data-integrity", Focus: "nomatch"},
			reason:  ReasonInventoryMismatch,
		},
		{name: "focus is last", options: Options{Focus: "nomatch"}, reason: ReasonFocusMismatch},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ApplyOptionsWithSelection(report, tt.options)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Items) != 1 || got.Items[0].Decision != DecisionFiltered || got.Items[0].ReasonCode != tt.reason {
				t.Fatalf("decisions = %#v, want %s", got.Items, tt.reason)
			}
			if len(got.Report.ReadQueue) != 0 {
				t.Fatalf("filtered queue = %#v", got.Report.ReadQueue)
			}
		})
	}
}

func TestApplyOptionsWithSelectionPreservesTopOrderAndInputs(t *testing.T) {
	t.Parallel()
	original := []ReadItem{
		{Rank: 1, EvidenceID: EvidenceIdentity(ReadItem{Path: "internal/auth/token.go", Score: 30, Why: []string{"risk:security"}}), Path: "internal/auth/token.go", Score: 30, Lane: LaneKept, Why: []string{"risk:security"}},
		{Rank: 2, EvidenceID: EvidenceIdentity(ReadItem{Path: "internal/auth/handler.go", Score: 20, Why: []string{"risk:security", "churn:2 commits"}}), Path: "internal/auth/handler.go", Score: 20, Lane: LaneKept, Why: []string{"risk:security", "churn:2 commits"}},
		{Rank: 3, EvidenceID: EvidenceIdentity(ReadItem{Path: "internal/auth/store.go", Score: 10, Why: []string{"risk:security"}}), Path: "internal/auth/store.go", Score: 10, Lane: LaneAlternate, Why: []string{"risk:security"}},
	}
	before := slices.Clone(original)
	before[0].Why = slices.Clone(original[0].Why)
	before[1].Why = slices.Clone(original[1].Why)
	before[2].Why = slices.Clone(original[2].Why)

	limited, err := ApplyOptionsWithSelection(Report{ReadQueue: original}, Options{Top: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Report.ReadQueue) != 2 || limited.Report.ReadQueue[0].Path != original[0].Path || limited.Report.ReadQueue[1].Path != original[1].Path {
		t.Fatalf("limited queue = %#v", limited.Report.ReadQueue)
	}
	wantDecisions := []struct {
		path         string
		decision     SelectionDecision
		reason       SelectionReason
		selectedRank int
	}{
		{original[0].Path, DecisionSelected, ReasonSelected, 1},
		{original[1].Path, DecisionSelected, ReasonSelected, 2},
		{original[2].Path, DecisionDeprioritized, ReasonTopLimit, 0},
	}
	for i, want := range wantDecisions {
		if limited.Items[i].Input.Path != want.path || limited.Items[i].Decision != want.decision ||
			limited.Items[i].ReasonCode != want.reason || limited.Items[i].SelectedRank != want.selectedRank {
			t.Fatalf("decision %d = %#v, want %#v", i, limited.Items[i], want)
		}
	}

	unlimited, err := ApplyOptionsWithSelection(Report{ReadQueue: original}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range unlimited.Items {
		if item.Decision != DecisionSelected || item.ReasonCode != ReasonSelected || item.SelectedRank != i+1 {
			t.Fatalf("unlimited decision %d = %#v", i, item)
		}
	}
	if len(unlimited.Report.ReadQueue) != len(original) {
		t.Fatalf("unlimited queue = %#v", unlimited.Report.ReadQueue)
	}
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("input changed:\n got %#v\nwant %#v", original, before)
	}
	limited.Items[0].Input.Why[0] = "mutated"
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("decision aliases caller memory: %#v", original)
	}
}

func TestApplyOptionsKeepsCullLanesAsClassification(t *testing.T) {
	t.Parallel()
	original := []ReadItem{
		{Rank: 1, Path: "internal/auth/token_test.go", Score: 30, Lane: LaneTest, Why: []string{"risk:security", "churn:2 commits"}},
		{Rank: 2, Path: "docs/guide.md", Score: 25, Lane: LaneDocs, Why: []string{"risk:security", "churn:2 commits"}},
		{Rank: 3, Path: "internal/generated/code.pb.go", Score: 20, Lane: LaneGenerated, Why: []string{"risk:security", "churn:2 commits"}},
	}
	for i := range original {
		withoutLane := original[i]
		withoutLane.Lane = ""
		original[i].EvidenceID = EvidenceIdentity(withoutLane)
	}
	got, err := ApplyOptionsWithSelection(Report{ReadQueue: original, CullLedger: &CullLedger{Schema: CullLedgerSchema}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	wantLanes := []string{LaneTest, LaneDocs, LaneGenerated}
	for i, lane := range wantLanes {
		if got.Report.ReadQueue[i].Lane != lane || got.Items[i].Input.Lane != lane || got.Items[i].Decision != DecisionSelected {
			t.Fatalf("row %d queue=%#v decision=%#v want lane %s", i, got.Report.ReadQueue[i], got.Items[i], lane)
		}
	}
	if len(got.Report.CullLedger.Entries) != len(original) || got.Report.CullLedger.Counts[LaneTest] != 1 ||
		got.Report.CullLedger.Counts[LaneDocs] != 1 || got.Report.CullLedger.Counts[LaneGenerated] != 1 {
		t.Fatalf("cull ledger = %#v", got.Report.CullLedger)
	}
}

func TestComposeCapPreventsRow101Selection(t *testing.T) {
	t.Parallel()
	paths := make([]string, 101)
	for i := range paths {
		paths[i] = fmt.Sprintf("pkg/file-%03d.go", i)
	}
	report := Compose(Packets{Paths: paths}, 0)
	if len(report.ReadQueue) != maxReadQueue || report.ReadQueue[99].Path != paths[99] {
		t.Fatalf("composed queue length=%d last=%q", len(report.ReadQueue), report.ReadQueue[99].Path)
	}
	options := Options{Include: []string{paths[100]}}
	selection, err := ApplyOptionsWithSelection(report, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Report.ReadQueue) != 0 || len(selection.Items) != maxReadQueue {
		t.Fatalf("selection queue=%d items=%d", len(selection.Report.ReadQueue), len(selection.Items))
	}
	for _, item := range selection.Items {
		if item.Input.Path == paths[100] {
			t.Fatalf("upstream-omitted row received a decision: %#v", item)
		}
	}
	document, err := NewSelectionDocument(analyze.Status{Complete: true}, report, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Items) != maxReadQueue || document.Summary.Candidates != maxReadQueue {
		t.Fatalf("document items=%d summary=%#v", len(document.Items), document.Summary)
	}
}
