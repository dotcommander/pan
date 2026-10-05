package retrieval

import (
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestFindReportGuidanceOnPartialMissNamesLimits(t *testing.T) {
	t.Parallel()
	report := NewFindReportWithStatus(
		FindQuery{Name: "Missing"},
		nil,
		analyze.Status{Complete: false, Limits: []string{"max_file_bytes", "max_nodes"}},
	)
	if report.Outcome != FindOutcomeNoMatchInPartial {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	if len(report.Matches) != 0 {
		t.Fatalf("matches = %#v", report.Matches)
	}
	joined := strings.Join(report.Guidance, "\n")
	if !strings.Contains(joined, "max_file_bytes, max_nodes") || !strings.Contains(joined, "not proof the symbol is absent") {
		t.Fatalf("guidance missing limits/absence framing: %#v", report.Guidance)
	}
	if !strings.Contains(joined, "pan scan doctor") {
		t.Fatalf("guidance missing doctor step: %#v", report.Guidance)
	}
	if len(report.Guidance) > maxFindGuidance {
		t.Fatalf("guidance exceeds bound: %#v", report.Guidance)
	}
}

func TestFindReportGuidanceOnCompleteMissSkipsAbsenceFraming(t *testing.T) {
	t.Parallel()
	report := NewFindReportWithStatus(FindQuery{Name: "Missing"}, nil, analyze.Status{Complete: true})
	if report.Outcome != FindOutcomeNoMatchInScope {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	joined := strings.Join(report.Guidance, "\n")
	if strings.Contains(joined, "not proof the symbol is absent") {
		t.Fatalf("complete-scope miss must not claim incompleteness: %#v", report.Guidance)
	}
	if !strings.Contains(joined, "check spelling") {
		t.Fatalf("guidance missing spelling step: %#v", report.Guidance)
	}
}

func TestFindReportGuidanceMentionsFiltersWhenActive(t *testing.T) {
	t.Parallel()
	report := NewFindReportWithStatus(
		FindQuery{Name: "Missing", Kind: "function", File: "cmd/"},
		nil,
		analyze.Status{Complete: true},
	)
	joined := strings.Join(report.Guidance, "\n")
	if !strings.Contains(joined, `kind="function"`) || !strings.Contains(joined, `file="cmd/"`) {
		t.Fatalf("guidance missing effective filters: %#v", report.Guidance)
	}
}

func TestFindReportGuidanceAbsentOnFound(t *testing.T) {
	t.Parallel()
	match := SymbolMatch{File: "a.go", Symbol: analyze.Symbol{Name: "Found", Kind: "function", Location: analyze.Location{Line: 3}}, Score: 100, Basis: BasisExact}
	report := NewFindReportWithStatus(FindQuery{Name: "Found"}, []SymbolMatch{match}, analyze.Status{Complete: true})
	if report.Outcome != FindOutcomeFound || report.Guidance != nil {
		t.Fatalf("report = %#v", report)
	}
}

func TestNewFindReportCompatDelegatesWithCompleteStatus(t *testing.T) {
	t.Parallel()
	report := NewFindReport(FindQuery{Name: "Missing"}, nil, true)
	if report.Outcome != FindOutcomeNoMatchInScope {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	if len(report.Guidance) == 0 {
		t.Fatal("compat constructor must still derive guidance")
	}
}
