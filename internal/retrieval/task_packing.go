package retrieval

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

const taskSelectionWorkCap = 96

// packTaskTargets tries feasible direct evidence for distinct terms before
// filling the remainder in normal rank order. Failed candidates do not stop
// selection or consume the embedded-source cap.
func packTaskTargets(report *TaskReport, candidates []taskCandidate, snap analyze.Snapshot, ranked []ranking.RankedFile, consumed []string) error {
	terms := goalTerms(report.Goal)
	supported := make(map[string]bool)
	for _, candidate := range candidates {
		for _, term := range terms {
			if candidateMatchesTerm(candidate, term) {
				supported[term] = true
			}
		}
	}
	covered := make(map[string]bool)
	attempted := make(map[string]bool)
	try := func(candidate taskCandidate) error {
		if attempted[candidate.file.Path] || len(attempted) >= taskSelectionWorkCap || len(report.Targets) >= taskTargetLimit {
			return nil
		}
		attempted[candidate.file.Path] = true
		matched := candidateTerms(candidate, terms)
		nextCovered := make(map[string]bool, len(covered)+len(matched))
		for term := range covered {
			nextCovered[term] = true
		}
		for _, term := range matched {
			nextCovered[term] = true
		}
		accepted, err := tryTaskTarget(report, candidate, snap, ranked, consumed, len(candidates), len(supported), len(nextCovered))
		if err != nil {
			return err
		}
		if accepted {
			covered = nextCovered
		}
		return nil
	}
	for _, term := range terms {
		if !supported[term] || covered[term] || len(attempted) >= taskSelectionWorkCap || len(report.Targets) == taskTargetLimit {
			continue
		}
		for _, candidate := range candidates {
			if len(attempted) >= taskSelectionWorkCap || len(report.Targets) == taskTargetLimit || covered[term] {
				break
			}
			if candidateMatchesTerm(candidate, term) {
				if err := try(candidate); err != nil {
					return err
				}
			}
		}
	}
	for _, candidate := range candidates {
		if len(attempted) >= taskSelectionWorkCap || len(report.Targets) == taskTargetLimit {
			break
		}
		if err := try(candidate); err != nil {
			return err
		}
	}
	if len(report.Targets) < len(candidates) {
		report.addTruncation("targets", len(report.Targets), len(candidates), "target or token budget")
	}
	if len(covered) < len(supported) {
		report.addTruncation("goal_terms", len(covered), len(supported), "target or token budget")
	}
	if len(attempted) == taskSelectionWorkCap && len(attempted) < len(candidates) {
		report.addTruncation("candidate_work", len(attempted), len(candidates), "selection work cap")
	}
	return nil
}

func candidateMatchesTerm(candidate taskCandidate, term string) bool {
	if candidate.fallback {
		return false
	}
	_, _, score := fieldEvidence(&candidate.file, []string{term})
	return score > 0
}

func candidateTerms(candidate taskCandidate, terms []string) []string {
	var matched []string
	for _, term := range terms {
		if candidateMatchesTerm(candidate, term) {
			matched = append(matched, term)
		}
	}
	return matched
}

// tryTaskTarget charges the complete packet, reserving the final aggregate
// disclosures before accepting a target. A source-free complete target is the
// only smaller representation tried; fields within a target stay whole.
func tryTaskTarget(report *TaskReport, candidate taskCandidate, snap analyze.Snapshot, ranked []ranking.RankedFile, consumed []string, total, supported, covered int) (bool, error) {
	target, truncations := buildTaskTarget(snap, ranked, candidate, consumed)
	sources := 0
	for _, prior := range report.Targets {
		if len(prior.Source) > 0 {
			sources++
		}
	}
	if len(target.Source) > 0 && sources >= taskMaxSources {
		truncations = omitTaskSource(truncations, target.Path, len(target.Source), "source target cap")
		target.Source = nil
	}
	for {
		trial := *report
		trial.Targets = append(slices.Clone(report.Targets), target)
		trial.Truncations = append(slices.Clone(report.Truncations), truncations...)
		probe := trial
		if len(probe.Targets) < total {
			probe.addTruncation("targets", len(probe.Targets), total, "target or token budget")
		}
		if covered < supported {
			probe.addTruncation("goal_terms", covered, supported, "target or token budget")
		}
		if total > taskSelectionWorkCap && len(probe.Targets) < taskTargetLimit {
			probe.addTruncation("candidate_work", taskSelectionWorkCap, total, "selection work cap")
		}
		if err := finalizeTaskReport(&probe); err != nil {
			return false, err
		}
		if probe.Budget.UsedTokens <= probe.Budget.MaxTokens {
			if err := finalizeTaskReport(&trial); err != nil {
				return false, err
			}
			*report = trial
			return true, nil
		}
		if len(target.Source) == 0 {
			return false, nil
		}
		truncations = omitTaskSource(truncations, target.Path, len(target.Source), "token budget")
		target.Source = nil
	}
}

func omitTaskSource(truncations []analyze.Truncation, path string, count int, reason string) []analyze.Truncation {
	prefix := fmt.Sprintf("targets[%s].source", path)
	kept := make([]analyze.Truncation, 0, len(truncations)+1)
	for _, truncation := range truncations {
		if !strings.HasPrefix(truncation.Field, prefix) {
			kept = append(kept, truncation)
		}
	}
	return append(kept, taskSourceOmission(path, count, reason))
}

func finalizeTaskReport(report *TaskReport) error {
	report.Selection.Selected = len(report.Targets)
	report.ReadNext = taskReadNext(report.Targets)
	report.VerifyCommands = taskVerifyCommands(report.Targets)
	report.FollowUpCommands = nil
	if len(report.Truncations) > 0 {
		report.FollowUpCommands = []string{fmt.Sprintf("pan context map --intent %q for the full ranked map", report.Goal)}
	}
	return setTaskUsedTokens(report)
}
