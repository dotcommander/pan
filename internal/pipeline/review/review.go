// Package review checks a Pan spec against the codebase for drift.
package review

import (
	"context"
	"fmt"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// Severity ranks a finding's urgency.
type Severity int

const (
	// Info is an informational finding: nothing is wrong yet.
	Info Severity = iota
	// Medium is a moderate finding worth reviewing soon.
	Medium
	// High is an important finding that should be addressed.
	High
	// Critical is an urgent finding that undermines the spec's accuracy.
	Critical
)

// String returns the lowercase label for a severity level.
func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Medium:
		return "medium"
	case High:
		return "high"
	case Critical:
		return "critical"
	default:
		return "info"
	}
}

// Check names stamped on findings by each detector. Shared constants keep
// the emitted names stable across detectors and tests.
const (
	checkNameExistence          = "existence"
	checkNamePhaseOrphan        = "phase-orphan"
	checkNameSymbolCoverage     = "symbol-coverage"
	checkNameStoreFreshness     = "store-freshness"
	checkNameCoverageGap        = "coverage-gap"
	checkNameCoverageConfidence = "coverage-confidence"
)

// Finding is a single drift observation.
type Finding struct {
	// Check is one of the check* constants in this file ("existence",
	// "symbol-coverage", "store-freshness", "coverage-gap", "phase-orphan").
	Check    string
	Severity Severity
	Phase    string // phase name (empty for global findings)
	Message  string
}

// Report collects all findings for a spec review.
type Report struct {
	SpecPath string
	Findings []Finding
}

// HasSeverity reports whether any finding has severity >= min.
func (r *Report) HasSeverity(min Severity) bool {
	for _, f := range r.Findings {
		if f.Severity >= min {
			return true
		}
	}
	return false
}

// CountBySeverity returns the number of findings at exactly sev.
func (r *Report) CountBySeverity(sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

// Review checks a spec against the codebase represented by ranked Pan files.
// root is the absolute project root. ranked is the full Pan output.
func Review(_ context.Context, s *spec.Spec, root string, ranked []symbols.RankedFile) *Report {
	report := &Report{SpecPath: root}

	symIdx := buildSymbolIndex(root, ranked)

	report.Findings = append(report.Findings, checkExistence(s, root)...)
	report.Findings = append(report.Findings, checkPhaseOrphans(s, root)...)
	report.Findings = append(report.Findings, checkSymbolCoverage(s, root, symIdx)...)
	report.Findings = append(report.Findings, checkStoreFreshness(s, symIdx)...)
	report.Findings = append(report.Findings, checkCoverageGap(s, root, ranked)...)
	if s.Coverage.IsLow() {
		report.Findings = append(report.Findings, Finding{
			Check:    checkNameCoverageConfidence,
			Severity: High,
			Message: fmt.Sprintf("scan coverage is too low for strict architecture review: %d/%d files represented",
				s.Coverage.Represented, s.Coverage.Total),
		})
	}

	return report
}
