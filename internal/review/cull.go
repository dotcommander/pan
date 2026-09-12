package review

import (
	"strings"
)

// CullLedgerSchema is stamped on every cull ledger.
const CullLedgerSchema = "pan.cull/v1"

// Deterministic cull lanes. Every read-queue row is classified into exactly
// one lane; production review starts with kept, alternate rows are the next
// targets, and the remaining lanes are separated from the production queue.
const (
	LaneKept      = "kept"
	LaneAlternate = "alternate"
	LaneTest      = "test"
	LaneDocs      = "docs"
	LaneGenerated = "generated"
	LaneLowSignal = "low-signal"
)

// Cull thresholds: a row needs corroboration from at least keptReasons
// independent signals at or above keptScore to seed production review;
// weaker multi-signal rows stay alternates.
const (
	keptScore      = 20
	keptReasons    = 2
	alternateScore = 8
)

// Disposition is one row's deterministic cull classification.
type Disposition struct {
	Lane   string
	Reason string
}

// CullEntry is one classified read-queue row in a cull ledger.
type CullEntry struct {
	Rank   int    `json:"rank"`
	Path   string `json:"path"`
	Score  int    `json:"score"`
	Lane   string `json:"lane"`
	Reason string `json:"reason"`
}

// CullLedger separates the read queue into deterministic review lanes with
// per-lane counts. Entries stay bounded because the read queue itself is
// bounded.
type CullLedger struct {
	Schema  string         `json:"schema"`
	Counts  map[string]int `json:"counts"`
	Entries []CullEntry    `json:"entries"`
}

// cullLaneOrder returns the canonical lane order used for counts and
// validation, rebuilt per caller so the package owns no global tables.
func cullLaneOrder() []string {
	return []string{LaneKept, LaneAlternate, LaneTest, LaneDocs, LaneGenerated, LaneLowSignal}
}

// ValidLane reports whether lane is a canonical cull lane.
func ValidLane(lane string) bool {
	for _, name := range cullLaneOrder() {
		if lane == name {
			return true
		}
	}
	return false
}

// CullDispositions classifies every read-queue row deterministically. The
// classification is a pure function of the row: path shape first, then the
// corroboration thresholds over score and reasons.
func CullDispositions(items []ReadItem) []Disposition {
	dispositions := make([]Disposition, len(items))
	for i, item := range items {
		dispositions[i] = cullDisposition(item)
	}
	return dispositions
}

func cullDisposition(item ReadItem) Disposition {
	lower := strings.ToLower(item.Path)
	switch {
	case isGeneratedPath(lower):
		return Disposition{LaneGenerated, "generated, minified, or derived artifact path"}
	case isCullTestPath(lower):
		return Disposition{LaneTest, "test or fixture path separated from the production queue"}
	case isDocsPath(lower):
		return Disposition{LaneDocs, "documentation path separated from the production queue"}
	case item.Score >= keptScore && len(item.Why) >= keptReasons:
		return Disposition{LaneKept, "multi-signal production review seed"}
	case item.Score >= alternateScore:
		return Disposition{LaneAlternate, "plausible next review target"}
	default:
		return Disposition{LaneLowSignal, "weak evidence intersection below the alternate threshold"}
	}
}

// BuildCullLedger classifies the read queue and returns the bounded ledger:
// per-lane counts plus one entry per row in rank order.
func BuildCullLedger(items []ReadItem) *CullLedger {
	dispositions := CullDispositions(items)
	ledger := &CullLedger{Schema: CullLedgerSchema, Counts: map[string]int{}, Entries: make([]CullEntry, 0, len(items))}
	for i, item := range items {
		disposition := dispositions[i]
		ledger.Counts[disposition.Lane]++
		ledger.Entries = append(ledger.Entries, CullEntry{
			Rank:   item.Rank,
			Path:   item.Path,
			Score:  item.Score,
			Lane:   disposition.Lane,
			Reason: disposition.Reason,
		})
	}
	return ledger
}

func isGeneratedPath(lower string) bool {
	return strings.Contains(lower, "/generated/") ||
		strings.Contains(lower, ".gen.") ||
		strings.Contains(lower, ".generated.") ||
		strings.HasSuffix(lower, ".pb.go") ||
		strings.HasSuffix(lower, ".min.js") ||
		strings.HasSuffix(lower, ".bundle.js")
}

func isCullTestPath(lower string) bool {
	return strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/testdata/") ||
		strings.Contains(lower, "/fixtures/") ||
		strings.HasPrefix(lower, "test/") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.HasPrefix(lower, "testdata/") ||
		strings.HasPrefix(lower, "fixtures/") ||
		strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, ".test.js") ||
		strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".spec.js")
}

func isDocsPath(lower string) bool {
	return strings.HasPrefix(lower, "docs/") ||
		strings.HasPrefix(lower, "doc/") ||
		strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".mdx") ||
		strings.HasSuffix(lower, ".rst")
}
