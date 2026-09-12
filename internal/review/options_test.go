package review

import (
	"slices"

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
