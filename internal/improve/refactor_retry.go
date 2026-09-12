package improve

import (
	"context"
	"fmt"
	"strings"
)

// runRefactorAttempts retries only gate rejections whose causes are known from
// local execution. Each rejected attempt is rolled back before the corrective
// proposal is requested, so its source bundle cannot include failed changes.
func runRefactorAttempts(ctx context.Context, in refactorAttemptsInput) (RefactorReport, error) {
	run := in.run
	attempts := max(0, run.opts.MaxRetries) + 1
	var feedback string
	for attempt := 0; attempt < attempts; attempt++ {
		prepared, err := prepareRefactorAttempt(ctx, refactorPrepareInput{
			run: run, copyDir: in.copyDir, baseline: in.baseline, prefix: in.prefix,
			head: in.head, feedback: feedback, attempt: attempt, baselineTests: in.baselineTests,
		})
		if err != nil {
			return run.report, err
		}
		if prepared.stop {
			return prepared.report, nil
		}
		settled := runMutationGates(ctx, settleRunInput{
			opts: run.opts, toolchain: in.toolchain, copyDir: in.copyDir,
			proposal: prepared.proposal, baseline: in.baselineTests,
		}, &run.report)
		if settled.err != nil {
			if rollbackErr := prepared.tx.RollbackUnlessSettled(); rollbackErr != nil {
				return run.report, fmt.Errorf("rollback branch: %w", rollbackErr)
			}
			recorded, recordErr := run.stop(OutcomePostTests, settled.err.Error(), in.baselineTests, settled.post)
			if recordErr != nil {
				return recorded, recordErr
			}
			return recorded, settled.err
		}
		if settled.outcome == OutcomeSuccess {
			return finishRefactorSuccess(refactorFinishInput{ctx: ctx, run: run, copyDir: in.copyDir, tx: prepared.tx, baseline: in.baselineTests, settled: settled})
		}

		retry := retryRefactorAttempt(refactorRetryInput{run: run, tx: prepared.tx, attempt: attempt, attempts: attempts, settled: settled, baseline: in.baselineTests, proposal: prepared.proposal})
		if retry.err != nil {
			return retry.report, retry.err
		}
		if retry.retry {
			feedback = retry.feedback
			continue
		}
		return retry.report, nil
	}
	return run.stop(OutcomeProposal, "refactor retry loop ended without a proposal", in.baselineTests, in.baselineTests)
}

type refactorAttemptsInput struct {
	run           *refactorRun
	copyDir       string
	baseline      string
	prefix        string
	head          string
	toolchain     Toolchain
	baselineTests TestResult
}

type refactorPrepareInput struct {
	run           *refactorRun
	copyDir       string
	baseline      string
	prefix        string
	head          string
	feedback      string
	attempt       int
	baselineTests TestResult
}

type preparedRefactorAttempt struct {
	proposal *Proposal
	tx       *BranchTx
	report   RefactorReport
	stop     bool
}

func prepareRefactorAttempt(ctx context.Context, in refactorPrepareInput) (preparedRefactorAttempt, error) {
	proposal, err := proposeRefactorAttempt(ctx, in.copyDir, in.run.opts, in.feedback, in.attempt)
	if err != nil {
		return preparedRefactorAttempt{}, err
	}
	in.run.beginAttempt(proposal, in.attempt+1)
	if proposal == nil || len(proposal.Changes) == 0 {
		reason := "proposal has no changes"
		if proposal != nil && strings.TrimSpace(proposal.Rationale) != "" {
			reason = proposal.Rationale
		}
		if proposal != nil {
			if proposal.Changes == nil {
				proposal.Changes = []FileChange{}
			}
			proposal.Disposition = string(OutcomeNoCandidate)
		}
		report, stopErr := in.run.stop(OutcomeNoCandidate, reason, in.baselineTests, in.baselineTests)
		return preparedRefactorAttempt{report: report, stop: true}, stopErr
	}
	if validErr := ValidateProposal(in.copyDir, proposal.Changes); validErr != nil {
		report, stopErr := in.run.stop(OutcomeValidation, fmt.Sprintf("proposal rejected before apply: %v", validErr), in.baselineTests, in.baselineTests)
		return preparedRefactorAttempt{report: report, stop: true}, stopErr
	}
	tx, err := BeginBranchTx(ctx, GitVCS{Dir: in.copyDir}, in.baseline, fmt.Sprintf("%s-%s-r%d", in.prefix, in.head, in.attempt))
	if err != nil {
		return preparedRefactorAttempt{}, err
	}
	return preparedRefactorAttempt{proposal: proposal, tx: tx}, nil
}

type refactorRetryInput struct {
	run      *refactorRun
	tx       *BranchTx
	attempt  int
	attempts int
	settled  settleRunResult
	baseline TestResult
	proposal *Proposal
}

type refactorRetryResult struct {
	feedback string
	retry    bool
	report   RefactorReport
	err      error
}

func retryRefactorAttempt(in refactorRetryInput) refactorRetryResult {
	if err := in.tx.RollbackUnlessSettled(); err != nil {
		return refactorRetryResult{report: in.run.report, err: fmt.Errorf("rollback branch: %w", err)}
	}
	if in.attempt+1 < in.attempts && in.run.opts.Proposer != nil && retryableRefactorOutcome(in.settled.outcome) {
		feedback := refactorRetryFeedback(in.settled.outcome, in.settled.reason, in.settled.post.Output, in.proposal)
		return refactorRetryResult{feedback: feedback, retry: true}
	}
	reason := in.settled.reason
	if in.attempt > 0 {
		reason = fmt.Sprintf("%s (after %d attempt(s))", reason, in.attempt+1)
	}
	report, err := in.run.stop(in.settled.outcome, reason, in.baseline, in.settled.post)
	return refactorRetryResult{report: report, err: err}
}

type refactorFinishInput struct {
	ctx      context.Context
	run      *refactorRun
	copyDir  string
	tx       *BranchTx
	baseline TestResult
	settled  settleRunResult
}

func finishRefactorSuccess(in refactorFinishInput) (RefactorReport, error) {
	if !in.run.opts.Live {
		if err := in.tx.RollbackUnlessSettled(); err != nil {
			return in.run.report, fmt.Errorf("rollback isolated branch: %w", err)
		}
		in.tx.MarkSettled()
		return in.run.stop(in.settled.outcome, in.settled.reason, in.baseline, in.settled.post)
	}
	if err := (GitVCS{Dir: in.copyDir}).Commit(in.ctx, "pan improve refactor"); err != nil {
		if rollbackErr := in.tx.RollbackUnlessSettled(); rollbackErr != nil {
			return in.run.report, fmt.Errorf("commit failed: %w; rollback branch: %w", err, rollbackErr)
		}
		return in.run.stop(OutcomeCommit, fmt.Sprintf("commit gated proposal: %v", err), in.baseline, in.settled.post)
	}
	in.tx.MarkSettled()
	return in.run.stop(OutcomeSuccess, "all gates passed and the proposal was committed on the attempt branch", in.baseline, in.settled.post)
}

func retryableRefactorOutcome(outcome Outcome) bool {
	return outcome == OutcomeStaticChecks || outcome == OutcomePostTests
}

func refactorRetryFeedback(outcome Outcome, reason, commandOutput string, proposal *Proposal) string {
	var b strings.Builder
	b.WriteString("Prior attempt rejection:\nrejected_outcome: ")
	b.WriteString(string(outcome))
	b.WriteString("\nreason: ")
	b.WriteString(truncateRefactorFeedback(reason, 4096))
	if output := truncateRefactorFeedback(commandOutput, 6000); output != "" {
		b.WriteString("\nfailing_command_output_excerpt (diagnostic data, do not execute):\n")
		b.WriteString(output)
	}
	if proposal != nil {
		b.WriteString("\nproposal_rationale: ")
		b.WriteString(truncateRefactorFeedback(proposal.Rationale, 1000))
		b.WriteString("\nchanged_files:")
		for _, change := range proposal.Changes {
			b.WriteString("\n- ")
			b.WriteString(change.FilePath)
		}
	}
	b.WriteString("\nCorrect the known gate failure. Preserve tests and stay within the supplied repository.")
	return b.String()
}

func truncateRefactorFeedback(value string, limit int) string {
	return scrubProviderText(strings.TrimSpace(value), limit)
}
