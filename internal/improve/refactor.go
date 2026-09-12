package improve

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RefactorSchema stamps every refactor run report.
const RefactorSchema = "pan.improve-refactor/v1"

// RefactorOptions configures one guarded refactor run. Zero TestTimeout
// selects the toolchain default; nil Now uses time.Now.
type RefactorOptions struct {
	RepoPath    string
	StateDir    string
	TestTimeout time.Duration
	// MinBaselineCoverage is the required baseline statement coverage as a
	// fraction in [0,1]. Zero preserves the low-level caller default of no
	// coverage-floor refusal.
	MinBaselineCoverage float64
	BranchPrefix        string
	Live                bool
	CoverageGate        bool
	CoverageDrop        float64
	MutationGate        bool
	MutationFloor       float64
	MutationRunner      MutationRunner
	Staticcheck         bool
	Exclude             []string
	Proposer            Proposer
	// MaxRetries permits corrective proposals after a known static-check or
	// post-test rejection. Zero performs one attempt.
	MaxRetries       int
	StrategyID       string
	Provider         string
	Model            string
	FeePerLine       float64
	DeadSymbolsFirst bool
	Now              func() time.Time
}

// RefactorReport is the result of one guarded refactor: Pan improvement's baseline,
// proposal, apply, vet, post-test, anti-cheat, coverage, mutation, and diff
// gates. Dry-runs use a temporary copy; explicit live runs settle only after
// the attempt branch commits successfully.
type RefactorReport struct {
	Schema             string        `json:"schema"`
	RunType            string        `json:"run_type"`
	RepoPath           string        `json:"repo_path"`
	RepoHead           string        `json:"repo_head,omitempty"`
	Success            bool          `json:"success"`
	Outcome            string        `json:"outcome"`
	Reason             string        `json:"reason"`
	DryRun             bool          `json:"dry_run"`
	LiveApply          string        `json:"live_apply"`
	Audit              *AuditContext `json:"audit,omitempty"`
	Proposal           *Proposal     `json:"proposal,omitempty"`
	LinesAdded         int           `json:"lines_added,omitempty"`
	LinesDeleted       int           `json:"lines_deleted,omitempty"`
	NetLineReduction   int           `json:"net_line_reduction,omitempty"`
	SymbolsDeleted     int           `json:"symbols_deleted,omitempty"`
	SymbolsAdded       int           `json:"symbols_added,omitempty"`
	SymbolsModified    int           `json:"symbols_modified,omitempty"`
	NetSymbolReduction int           `json:"net_symbol_reduction,omitempty"`
	StructuralMetrics  bool          `json:"structural_metrics"`
	ChangedFiles       []string      `json:"changed_files,omitempty"`
	BaselineCoverage   float64       `json:"baseline_coverage_percent,omitempty"`
	FinalCoverage      float64       `json:"final_coverage_percent,omitempty"`
	BaselineTests      int           `json:"baseline_tests,omitempty"`
	PostTests          int           `json:"post_tests,omitempty"`
	MutationScore      float64       `json:"mutation_score,omitempty"`
	HistoryPath        string        `json:"history_path"`
	Attempts           int           `json:"attempts"`
}

const isolatedDryRun = "dry-run; applied only on a disposable branch and rolled back before reporting"

// RunRefactor executes one guarded refactor run and appends the
// ledger record. The sequence is fixed: preflight, isolated copy, green
// baseline, proposal generation, pre-apply validation, apply into the
// copy, vet on changed packages, post-change suite, test-identity and
// skip anti-cheat gates, coverage/mutation gates, and measured diff. Static-check
// and post-test rejections may request a bounded corrective proposal after
// rollback. Dry-runs use an isolated copy; live runs commit only after every
// gate passes.
func RunRefactor(ctx context.Context, opts RefactorOptions) (RefactorReport, error) {
	run := newRefactorRun(opts)
	tree, err := CheckWorkTree(ctx, opts.RepoPath)
	if err != nil {
		return run.report, err
	}
	run.report.RepoHead = tree.Head
	if !tree.OK() {
		return run.stop(OutcomePreflight, tree.Reason, TestResult{}, TestResult{})
	}
	if lookErr := LookGo(); lookErr != nil {
		return run.report, lookErr
	}
	if opts.Staticcheck {
		if lookErr := LookStaticcheck(); lookErr != nil {
			return run.report, lookErr
		}
	}

	var copyDir string
	if !opts.Live {
		copyDir, err = IsolateWorkTree(opts.RepoPath)
		if err != nil {
			return run.report, err
		}
		defer removeTree(copyDir)
	} else {
		var cleanup func() error
		copyDir, cleanup, err = IsolateLinkedWorkTree(ctx, opts.RepoPath, tree.Head)
		if err != nil {
			return run.report, err
		}
		defer func() { _ = cleanup() }()
	}
	toolchain := Toolchain{TestTimeout: opts.TestTimeout}
	run.report.Audit = BuildAuditContext(ctx, copyDir, opts.Exclude)

	baseline, err := toolchain.RunTests(ctx, copyDir)
	if err != nil {
		return run.report, fmt.Errorf("baseline suite: %w", err)
	}
	if !baseline.AllPassed {
		return run.stop(OutcomeBaselineTests, "baseline tests not green; refusing to attribute breakage to a proposal", baseline, TestResult{})
	}
	if reason := baselineCoverageFloorReason(baseline, opts.MinBaselineCoverage); reason != "" {
		return run.stop(OutcomeBaselineCoverage, reason, baseline, TestResult{})
	}

	isolatedBaseline := tree.Head
	if !opts.Live {
		isolatedBaseline, err = InitIsolatedRepository(ctx, copyDir)
		if err != nil {
			return run.report, err
		}
	}
	prefix := opts.BranchPrefix
	if prefix == "" {
		prefix = "pan-improve"
	}
	return runRefactorAttempts(ctx, refactorAttemptsInput{
		run: run, copyDir: copyDir, baseline: isolatedBaseline, prefix: prefix,
		head: tree.Head, toolchain: toolchain, baselineTests: baseline,
	})
}

func baselineCoverageFloorReason(result TestResult, floor float64) string {
	if floor <= 0 {
		return ""
	}
	floorPercent := floor * 100
	if !result.ProfileMeasured {
		return fmt.Sprintf("baseline coverage is unavailable; run pan improve prep first to reach the %.1f%% coverage floor", floorPercent)
	}
	if result.TotalStmts == 0 {
		return fmt.Sprintf("baseline coverage has zero measured statements; run pan improve prep first to reach the %.1f%% coverage floor", floorPercent)
	}
	if result.TotalCoverage < floorPercent {
		return fmt.Sprintf("baseline coverage %.1f%% is below the %.1f%% floor; run pan improve prep first", result.TotalCoverage, floorPercent)
	}
	return ""
}

func proposeRefactorAttempt(ctx context.Context, copyDir string, opts RefactorOptions, feedback string, attempt int) (*Proposal, error) {
	audit := BuildAuditContext(ctx, copyDir, opts.Exclude)
	if opts.Proposer == nil || (opts.DeadSymbolsFirst && attempt == 0) {
		proposal, err := ProposeDeadCode(copyDir, opts.Exclude)
		if err != nil {
			return nil, fmt.Errorf("deterministic proposal: %w", err)
		}
		if opts.Proposer == nil || len(proposal.Changes) > 0 {
			return proposal, nil
		}
	}
	summary := fmt.Sprintf("Repository copy: %s. Return a minimal whole-file refactor proposal. Do not modify tests, configuration, or files outside the repository.", copyDir)
	if feedback != "" {
		summary += "\n\n=== CORRECTION REQUIRED ===\n" + feedback
	}
	proposer := opts.Proposer
	if attempt > 0 {
		switch provider := proposer.(type) {
		case ProviderProposer:
			if provider.Settings.EscalationModel != "" {
				provider.Model = provider.Settings.EscalationModel
				proposer = provider
			}
		case *ProviderProposer:
			if provider != nil && provider.Settings.EscalationModel != "" {
				copy := *provider
				copy.Model = copy.Settings.EscalationModel
				proposer = copy
			}
		}
	}
	repository, packet, repositoryAware, err := scopeRepositoryProposer(ctx, proposer, copyDir, opts.Exclude, "")
	if repositoryAware {
		if err != nil {
			return nil, fmt.Errorf("build candidate packet: %w", err)
		}
		proposal, err := repository.ProposeRepository(ctx, summary, packet)
		if err != nil {
			return nil, fmt.Errorf("provider proposal: %w", err)
		}
		if proposal == nil {
			return nil, errors.New("provider proposal: repository proposer returned nil proposal")
		}
		proposal.Audit = audit
		return proposal, nil
	}
	proposal, err := opts.Proposer.Propose(ctx, summary)
	if err != nil {
		return nil, fmt.Errorf("provider proposal: %w", err)
	}
	proposal.Audit = audit
	return proposal, nil
}

// runMutationGates applies one proposal on the current disposable attempt
// branch, then runs static, test, identity, and diff gates. Its caller owns
// transaction rollback on every returned outcome.
func runMutationGates(ctx context.Context, in settleRunInput, report *RefactorReport) settleRunResult {
	var err error
	in.structural, err = MeasureStructuralProposal(ctx, in.copyDir, in.proposal.Changes)
	if err != nil {
		return settleRunResult{outcome: OutcomeDiff, reason: fmt.Sprintf("structural diff: %v", err)}
	}
	in.linesAdded, in.linesDeleted = measureProposal(in.copyDir, in.proposal.Changes)
	if err := ApplyProposal(in.copyDir, in.proposal.Changes); err != nil {
		return settleRunResult{outcome: OutcomeApply, reason: fmt.Sprintf("apply into isolated branch failed: %v", err)}
	}
	changed := changedFiles(in.proposal)
	report.ChangedFiles = changed
	if err := in.toolchain.Vet(ctx, in.copyDir, ChangedPackages(changed)); err != nil {
		return settleRunResult{outcome: OutcomeStaticChecks, reason: fmt.Sprintf("static checks failed on changed packages: %v", err)}
	}
	if in.opts.Staticcheck {
		if err := in.toolchain.Staticcheck(ctx, in.copyDir, ChangedPackages(changed), changed); err != nil {
			return settleRunResult{outcome: OutcomeStaticChecks, reason: fmt.Sprintf("staticcheck failed on changed files: %v", err)}
		}
	}
	return runSettlement(ctx, in, report)
}
