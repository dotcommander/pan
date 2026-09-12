package improve

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TestGenerator supplies test-only whole-file changes for one coverage target.
// It is separate from refactor proposals because prep cannot alter production.
type TestGenerator interface {
	Generate(context.Context, PrepTarget, string) ([]FileChange, error)
}

type generatedPrepResult struct {
	Outcome          Outcome
	Reason           string
	Final            TestResult
	Attempts         int
	Files            []string
	TargetOutcomes   []PrepTargetContext
	FailureArtifacts []string
}

type prepGeneration struct {
	changes []FileChange
	err     error
}

// prepRequestWaitError marks a local pacing failure. Unlike a provider
// rejection, waiting could not submit a corrective request, so it ends the run.
type prepRequestWaitError struct{ err error }

func (e prepRequestWaitError) Error() string { return e.err.Error() }

func (e prepRequestWaitError) Unwrap() error { return e.err }

type generatedPrepState struct {
	dir         string
	branchBase  string
	toolchain   Toolchain
	baseline    TestResult
	current     TestResult
	accepted    []FileChange
	lastRequest time.Time
	started     time.Time
	opts        PrepOptions
	result      generatedPrepResult
}

type prepOutcomeInput struct {
	Target      PrepTarget
	Outcome     string
	Reason      string
	Round       int
	Rank        int
	Result      TestResult
	Provisional bool
	Artifact    string
}

type generatedPrepInput struct {
	Dir        string
	SourceHead string
	Baseline   TestResult
	Targets    []PrepTarget
	Options    PrepOptions
}

type prepTargetRun struct {
	Accepted bool
	Stop     bool
	Err      error
}

type prepCandidateRun struct {
	Result   TestResult
	Applied  bool
	Feedback string
	Err      error
}

type prepCandidateRejection struct {
	Target  PrepTarget
	Changes []FileChange
	Post    TestResult
	Retry   int
	Round   int
	Rank    int
}

const prepAcceptedOutcome = "accepted"

func runGeneratedPrep(ctx context.Context, input generatedPrepInput) (generatedPrepResult, error) {
	branchBase, err := prepBranchBase(ctx, input.Dir, input.SourceHead, input.Options.Live)
	if err != nil {
		return generatedPrepResult{Final: input.Baseline}, err
	}
	tx, err := beginPrepTransaction(ctx, input.Dir, branchBase, input.Options.BranchPrefix)
	if err != nil {
		return generatedPrepResult{Final: input.Baseline}, err
	}
	settled := false
	defer func() {
		if !settled {
			_ = tx.RollbackUnlessSettled()
		}
	}()

	state := newGeneratedPrepState(input.Dir, branchBase, input.Baseline, input.Options)
	if err := state.runRounds(ctx, input.Targets); err != nil {
		return state.result, err
	}
	if err := state.settleBatch(ctx); err != nil {
		return state.result, err
	}
	state.commitLive(ctx)
	if input.Options.Live && state.result.Outcome != OutcomePlanned && state.result.Outcome != OutcomeCommit {
		tx.MarkSettled()
		settled = true
	}
	state.finish()
	return state.result, nil
}

func prepBranchBase(ctx context.Context, dir, sourceHead string, live bool) (string, error) {
	if live {
		return sourceHead, nil
	}
	return InitIsolatedRepository(ctx, dir)
}

func beginPrepTransaction(ctx context.Context, dir, branchBase, prefix string) (*BranchTx, error) {
	if prefix == "" {
		prefix = "pan-improve"
	}
	return BeginBranchTx(ctx, GitVCS{Dir: dir}, branchBase, prefix+"-prep")
}

func newGeneratedPrepState(dir, branchBase string, baseline TestResult, opts PrepOptions) *generatedPrepState {
	return &generatedPrepState{
		dir: dir, branchBase: branchBase, toolchain: Toolchain{TestTimeout: opts.TestTimeout}, baseline: baseline,
		current: baseline, started: time.Now(), opts: opts, result: generatedPrepResult{Final: baseline},
	}
}

func (s *generatedPrepState) runRounds(ctx context.Context, targets []PrepTarget) error {
	acceptedTargets := make(map[string]bool)
	for round := 1; round <= prepRounds(s.opts); round++ {
		stop, err := s.runRound(ctx, targets, acceptedTargets, round)
		if err != nil || stop {
			return err
		}
	}
	return nil
}

func prepRounds(opts PrepOptions) int {
	if opts.TargetRounds > 0 {
		return opts.TargetRounds
	}
	return 1
}

func (s *generatedPrepState) runRound(ctx context.Context, targets []PrepTarget, acceptedTargets map[string]bool, round int) (bool, error) {
	roundTargets, generated := prepGenerationRound(ctx, targets, acceptedTargets, len(s.result.Files), s.opts)
	for rank, target := range roundTargets {
		if s.skipTarget(target, acceptedTargets) {
			continue
		}
		targetRun := s.runTarget(ctx, target, round, rank+1, generated[rank])
		if targetRun.Err != nil {
			return false, targetRun.Err
		}
		if targetRun.Accepted {
			acceptedTargets[target.File] = true
		}
		if targetRun.Stop || s.stopAfterAcceptance(targetRun.Accepted) {
			return true, nil
		}
	}
	return s.stopAfterRound(), nil
}

func (s *generatedPrepState) skipTarget(target PrepTarget, accepted map[string]bool) bool {
	return accepted[target.File] || (s.opts.MaxFiles > 0 && len(s.result.Files) >= s.opts.MaxFiles)
}

func (s *generatedPrepState) stopAfterAcceptance(accepted bool) bool {
	return s.current.TotalCoverage >= s.opts.Floor*100 || (!s.opts.ContinueToFloor && accepted)
}

func (s *generatedPrepState) stopAfterRound() bool {
	return s.current.TotalCoverage >= s.opts.Floor*100 || (!s.opts.ContinueToFloor && len(s.result.Files) > 0)
}

func (s *generatedPrepState) runTarget(ctx context.Context, target PrepTarget, round, rank int, initial *prepGeneration) prepTargetRun {
	feedback := ""
	for retry := 0; retry <= s.opts.MaxRetries; retry++ {
		if s.budgetExhausted(target, round, rank) {
			return prepTargetRun{Stop: true}
		}
		changes, err := s.generate(ctx, target, feedback, retry, initial)
		if err != nil {
			var waitErr prepRequestWaitError
			if errors.As(err, &waitErr) {
				return prepTargetRun{Err: waitErr.err}
			}
			feedback = s.recordGenerationFailure(target, err, retry, round, rank)
			continue
		}
		if validationFeedback, invalid, validateErr := s.validateCandidate(target, changes, retry, round, rank); validateErr != nil {
			return prepTargetRun{Err: validateErr}
		} else if invalid {
			feedback = validationFeedback
			continue
		}
		if s.exceedsFileLimit(changes) {
			feedback = "proposal exceeds the remaining max-files allowance"
			continue
		}
		candidate := s.applyAndTest(ctx, target, changes)
		if candidate.Err != nil {
			return prepTargetRun{Err: candidate.Err}
		}
		if !candidate.Applied {
			feedback = candidate.Feedback
			continue
		}
		rejection := prepCandidateRejection{Target: target, Changes: changes, Post: candidate.Result, Retry: retry, Round: round, Rank: rank}
		if rejected, rejectErr := s.rejectCandidate(rejection); rejectErr != nil {
			return prepTargetRun{Err: rejectErr}
		} else if rejected {
			feedback = candidateFeedback(candidate.Result, s.current.TotalCoverage)
			continue
		}
		s.accept(target, changes, candidate.Result, round, rank)
		return prepTargetRun{Accepted: true}
	}
	return prepTargetRun{}
}

func (s *generatedPrepState) budgetExhausted(target PrepTarget, round, rank int) bool {
	if s.opts.TimeBudget <= 0 || time.Since(s.started) < s.opts.TimeBudget {
		return false
	}
	s.addOutcome(prepOutcomeInput{Target: target, Outcome: "canceled", Reason: "prep time budget exhausted", Round: round, Rank: rank, Result: s.current})
	return true
}

func (s *generatedPrepState) generate(ctx context.Context, target PrepTarget, feedback string, retry int, initial *prepGeneration) ([]FileChange, error) {
	s.result.Attempts++
	if retry == 0 && initial != nil {
		return initial.changes, initial.err
	}
	if err := waitPrepRequest(ctx, &s.lastRequest, s.opts.RequestInterval); err != nil {
		return nil, prepRequestWaitError{err: err}
	}
	requestCtx, cancel := prepRequestContext(ctx, s.opts, feedback)
	s.lastRequest = time.Now()
	changes, err := s.opts.Generator.Generate(requestCtx, target, feedback)
	cancel()
	return changes, err
}

func (s *generatedPrepState) recordGenerationFailure(target PrepTarget, err error, retry, round, rank int) string {
	feedback := fmt.Sprintf("previous generation failed: %v", err)
	if retry == s.opts.MaxRetries {
		outcome := "generation_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			outcome = "request_expired"
		}
		s.addOutcome(prepOutcomeInput{Target: target, Outcome: outcome, Reason: feedback, Round: round, Rank: rank, Result: s.current})
	}
	return feedback
}

func (s *generatedPrepState) validateCandidate(target PrepTarget, changes []FileChange, retry, round, rank int) (string, bool, error) {
	err := ValidateTestChanges(s.dir, changes)
	if err == nil {
		return "", false, nil
	}
	feedback := fmt.Sprintf("previous generation was rejected: %v", err)
	artifact, artifactErr := s.writeArtifact(target, "validation_failed", feedback, changes, s.current.TotalCoverage)
	if artifactErr != nil {
		return "", false, artifactErr
	}
	if retry == s.opts.MaxRetries {
		s.addOutcome(prepOutcomeInput{Target: target, Outcome: "validation_failed", Reason: feedback, Round: round, Rank: rank, Result: s.current, Artifact: artifact})
	}
	return feedback, true, nil
}

func (s *generatedPrepState) exceedsFileLimit(changes []FileChange) bool {
	return s.opts.MaxFiles > 0 && len(s.result.Files)+len(changes) > s.opts.MaxFiles
}

func (s *generatedPrepState) applyAndTest(ctx context.Context, target PrepTarget, changes []FileChange) prepCandidateRun {
	if err := (GitVCS{Dir: s.dir}).ResetAttempt(ctx, s.branchBase); err != nil {
		return prepCandidateRun{Err: err}
	}
	if err := ApplyProposal(s.dir, s.accepted); err != nil {
		return prepCandidateRun{Err: err}
	}
	if err := ApplyProposal(s.dir, changes); err != nil {
		return prepCandidateRun{Feedback: fmt.Sprintf("previous generation could not be applied: %v", err)}
	}
	post, err := runPrepCandidateSuite(ctx, s.toolchain, s.dir, target, s.opts.BatchFullSuite)
	return prepCandidateRun{Result: post, Applied: true, Err: err}
}

func (s *generatedPrepState) rejectCandidate(input prepCandidateRejection) (bool, error) {
	if input.Post.AllPassed && (s.opts.BatchFullSuite || input.Post.TotalCoverage > s.current.TotalCoverage) {
		return false, nil
	}
	feedback := candidateFeedback(input.Post, s.current.TotalCoverage)
	artifact, err := s.writeArtifact(input.Target, "rejected", feedback, input.Changes, input.Post.TotalCoverage)
	if err != nil {
		return false, err
	}
	if input.Retry == s.opts.MaxRetries {
		s.addOutcome(prepOutcomeInput{Target: input.Target, Outcome: "rejected", Reason: feedback, Round: input.Round, Rank: input.Rank, Result: input.Post, Artifact: artifact})
	}
	return true, nil
}

func candidateFeedback(post TestResult, current float64) string {
	if !post.AllPassed {
		return "previous generation made the suite fail; correct the test"
	}
	return fmt.Sprintf("previous generation did not improve %.2f%% coverage", current)
}

func (s *generatedPrepState) writeArtifact(target PrepTarget, outcome, reason string, changes []FileChange, current float64) (string, error) {
	artifact, err := writePrepFailureArtifact(s.opts, prepArtifactInput{Target: target, Outcome: outcome, Reason: reason, Changes: changes, Baseline: s.result.Final.TotalCoverage, Current: current})
	if err == nil && artifact != "" {
		s.result.FailureArtifacts = append(s.result.FailureArtifacts, artifact)
	}
	return artifact, err
}

func (s *generatedPrepState) accept(target PrepTarget, changes []FileChange, post TestResult, round, rank int) {
	for _, change := range changes {
		s.result.Files = append(s.result.Files, change.FilePath)
	}
	s.accepted = append(s.accepted, changes...)
	if !s.opts.BatchFullSuite {
		s.current = post
	}
	s.addOutcome(prepOutcomeInput{Target: target, Outcome: prepAcceptedOutcome, Reason: "validated generated test", Round: round, Rank: rank, Result: post, Provisional: s.opts.BatchFullSuite})
}

func (s *generatedPrepState) settleBatch(ctx context.Context) error {
	if !s.opts.BatchFullSuite || len(s.result.Files) == 0 {
		return nil
	}
	post, err := s.toolchain.RunTests(ctx, s.dir)
	if err != nil {
		return fmt.Errorf("batch full suite: %w", err)
	}
	if !post.AllPassed || post.TotalCoverage <= s.baseline.TotalCoverage {
		reason := "batched generated tests did not pass the full coverage suite"
		if post.AllPassed {
			reason = fmt.Sprintf("batched generated tests did not improve %.2f%% coverage", s.baseline.TotalCoverage)
		}
		s.result.TargetOutcomes = rejectProvisionalOutcomes(s.result.TargetOutcomes, reason)
		s.result.Final = s.baseline
		s.result.Outcome, s.result.Reason = OutcomePlanned, reason
		return nil
	}
	s.current = post
	s.result.TargetOutcomes = settleProvisionalOutcomes(s.result.TargetOutcomes, s.baseline.TotalCoverage, post.TotalCoverage)
	return nil
}

func (s *generatedPrepState) commitLive(ctx context.Context) {
	if len(s.result.Files) == 0 || !s.opts.Live || s.result.Outcome == OutcomePlanned {
		return
	}
	if err := (GitVCS{Dir: s.dir}).Commit(ctx, "pan improve prep"); err != nil {
		s.result.Outcome, s.result.Reason = OutcomeCommit, fmt.Sprintf("commit generated tests: %v", err)
	}
}

func (s *generatedPrepState) finish() {
	if s.result.Outcome == OutcomeCommit || s.result.Outcome == OutcomePlanned {
		return
	}
	s.result.Final = s.current
	if len(s.result.Files) == 0 {
		s.result.Outcome = OutcomePlanned
		s.result.Reason = fmt.Sprintf("no generated test improved coverage after %d attempt(s)", s.result.Attempts)
		return
	}
	s.result.Outcome = OutcomeSuccess
	if s.current.TotalCoverage >= s.opts.Floor*100 {
		s.result.Reason = "generated tests reached the coverage floor"
	} else {
		s.result.Reason = fmt.Sprintf("generated tests increased coverage to %.2f%% below the %.2f%% floor", s.current.TotalCoverage, s.opts.Floor*100)
	}
	if s.opts.Live {
		s.result.Reason += " and were committed on the attempt branch"
	} else {
		s.result.Reason += " in the isolated attempt"
	}
}

func (s *generatedPrepState) addOutcome(input prepOutcomeInput) {
	s.result.TargetOutcomes = append(s.result.TargetOutcomes, prepOutcome(input))
}

func runPrepCandidateSuite(ctx context.Context, toolchain Toolchain, dir string, target PrepTarget, batched bool) (TestResult, error) {
	if batched {
		return toolchain.RunPackage(ctx, dir, target.File)
	}
	return toolchain.RunTests(ctx, dir)
}

func prepOutcome(input prepOutcomeInput) PrepTargetContext {
	return PrepTargetContext{RepoRelFile: input.Target.File, Outcome: input.Outcome, Reason: input.Reason, Round: input.Round, WorklistRank: input.Rank, Score: input.Target.Score, EstimatedLift: input.Target.EstimatedLiftPercent, BeforeCoverage: input.Target.CoveragePercent, AfterCoverage: input.Result.TotalCoverage, CoverageDelta: input.Result.TotalCoverage - input.Target.CoveragePercent, CoverageIncreased: input.Result.TotalCoverage > input.Target.CoveragePercent, Provisional: input.Provisional, ArtifactPath: input.Artifact}
}

func rejectProvisionalOutcomes(outcomes []PrepTargetContext, reason string) []PrepTargetContext {
	for i := range outcomes {
		if outcomes[i].Provisional {
			outcomes[i].Outcome = "rejected"
			outcomes[i].Reason = reason
			outcomes[i].Provisional = false
			outcomes[i].CoverageIncreased = false
		}
	}
	return outcomes
}

func settleProvisionalOutcomes(outcomes []PrepTargetContext, before, after float64) []PrepTargetContext {
	for i := range outcomes {
		if outcomes[i].Provisional {
			outcomes[i].AfterCoverage = after
			outcomes[i].CoverageDelta = after - before
			outcomes[i].CoverageIncreased = after > before
			outcomes[i].Provisional = false
		}
	}
	return outcomes
}
