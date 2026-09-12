package improve

import (
	"fmt"
	"time"
)

// refactorRun carries one guarded refactor run's options, ledger, and
// report so the terminal stop path stays a small method instead of a
// capturing closure.
type refactorRun struct {
	opts    RefactorOptions
	history *History
	report  RefactorReport
}

// newRefactorRun prepares the run report and its ledger, defaulting the
// run clock.
func newRefactorRun(opts RefactorOptions) *refactorRun {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	history := NewHistory(HistoryPath(opts.StateDir))
	return &refactorRun{
		opts:    opts,
		history: history,
		report: RefactorReport{
			Schema:      RefactorSchema,
			RunType:     RunTypeRefactor,
			RepoPath:    opts.RepoPath,
			DryRun:      !opts.Live,
			LiveApply:   modeDescription(opts.Live),
			HistoryPath: history.Path(),
		},
	}
}

func modeDescription(live bool) string {
	if live {
		return "live; committed after all gates on the attempt branch"
	}
	return isolatedDryRun
}

// stop stamps the terminal outcome onto the report, appends the ledger
// record, and returns the final report.
func (r *refactorRun) stop(outcome Outcome, reason string, baseline, post TestResult) (RefactorReport, error) {
	report := &r.report
	report.Outcome = string(outcome)
	report.Reason = reason
	report.Success = outcome == OutcomeSuccess || outcome == OutcomeNoCandidate
	report.BaselineCoverage = baseline.TotalCoverage
	report.BaselineTests = baseline.TotalTests
	report.FinalCoverage = post.TotalCoverage
	report.PostTests = post.TotalTests
	if err := r.history.Append(Record{
		StrategyID:         r.opts.StrategyID,
		Provider:           r.opts.Provider,
		Model:              r.opts.Model,
		FeeEarned:          (StructuralMetrics{NetSymbolReduction: report.NetSymbolReduction}).Fee(report.Success, r.opts.FeePerLine),
		Timestamp:          r.opts.Now(),
		RunType:            RunTypeRefactor,
		RepoPath:           r.opts.RepoPath,
		RepoHead:           report.RepoHead,
		Success:            report.Success,
		Outcome:            string(outcome),
		Reason:             reason,
		DryRun:             report.DryRun,
		LinesAdded:         report.LinesAdded,
		LinesDeleted:       report.LinesDeleted,
		NetReduction:       report.NetLineReduction,
		SymbolsDeleted:     report.SymbolsDeleted,
		SymbolsAdded:       report.SymbolsAdded,
		SymbolsModified:    report.SymbolsModified,
		NetSymbolReduction: report.NetSymbolReduction,
		StructuralMetrics:  report.StructuralMetrics,
		Candidates:         proposalCandidates(report.Proposal),
		Refactor:           &RefactorContext{CandidateSource: candidateSource(report.Proposal)},
		ChangedFiles:       report.ChangedFiles,
		BaselineCoverage:   baseline.TotalCoverage,
		FinalCoverage:      post.TotalCoverage,
	}); err != nil {
		return *report, fmt.Errorf("record refactor history: %w", err)
	}
	return *report, nil
}

func (r *refactorRun) beginAttempt(proposal *Proposal, attempt int) {
	report := &r.report
	report.Attempts = attempt
	report.Proposal = proposal
	report.LinesAdded = 0
	report.LinesDeleted = 0
	report.NetLineReduction = 0
	report.SymbolsDeleted = 0
	report.SymbolsAdded = 0
	report.SymbolsModified = 0
	report.NetSymbolReduction = 0
	report.StructuralMetrics = false
	report.ChangedFiles = nil
	report.MutationScore = 0
}

func candidateSource(proposal *Proposal) string {
	if proposal == nil {
		return ""
	}
	return proposal.Source
}

func proposalCandidates(proposal *Proposal) *CandidatePacket {
	if proposal == nil {
		return nil
	}
	return proposal.CandidatePacket
}
