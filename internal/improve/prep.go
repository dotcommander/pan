package improve

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"
)

// PrepSchema stamps every prep run report.
const PrepSchema = "pan.improve-prep/v1"

// PrepOptions configures one guarded prep run. Zero Floor selects the
// configured default; zero MaxTargets disables the worklist bound; nil
// Now uses time.Now.
type PrepOptions struct {
	RepoPath          string
	StateDir          string
	Floor             float64 // fraction in [0,1]
	MaxTargets        int
	TestTimeout       time.Duration
	Exclude           []string
	Generator         TestGenerator
	MaxRetries        int
	RequestTimeout    time.Duration
	RequestInterval   time.Duration
	CorrectiveTimeout time.Duration
	TimeBudget        time.Duration
	ContinueToFloor   bool
	MaxFiles          int
	// TargetRounds retries unaccepted targets in a fresh worklist round. Zero
	// selects one round.
	TargetRounds int
	// MaxConcurrent bounds source-style parallel first-pass generation when
	// continuing across several targets. Zero selects four requests.
	MaxConcurrent int
	// BatchFullSuite validates each candidate's package first and defers the
	// authoritative repository-wide coverage suite until settlement.
	BatchFullSuite bool
	// FailureDir stores bounded, scrubbed rejected-attempt packets. Empty uses
	// StateDir/failures/prep when StateDir is set.
	FailureDir   string
	Live         bool
	BranchPrefix string
	StrategyID   string
	Provider     string
	Model        string
	Now          func() time.Time
}

// PrepTarget is one ranked test-prep worklist entry: a source file below
// the coverage floor with its uncovered statement gaps. Prep plans
// targets; it never generates or applies test files.
type PrepTarget struct {
	File                 string  `json:"file"`
	CoveragePercent      float64 `json:"coverage_percent"`
	Deficit              float64 `json:"deficit"`
	UncoveredStatements  int     `json:"uncovered_statements"`
	Gaps                 []Gap   `json:"gaps,omitempty"`
	Score                float64 `json:"score"`
	EstimatedLiftPercent float64 `json:"estimated_lift_percent"`
}

// PrepReport is the result of one guarded prep run. It plans or generates
// test-only changes in an isolated copy and can settle accepted changes in a
// live run after the configured gates pass.
type PrepReport struct {
	Schema            string              `json:"schema"`
	RunType           string              `json:"run_type"`
	RepoPath          string              `json:"repo_path"`
	RepoHead          string              `json:"repo_head,omitempty"`
	Success           bool                `json:"success"`
	Outcome           string              `json:"outcome"`
	Reason            string              `json:"reason"`
	DryRun            bool                `json:"dry_run"`
	LiveApply         string              `json:"live_apply"`
	Floor             float64             `json:"floor"`
	FloorPercent      float64             `json:"floor_percent"`
	BaselineCoverage  float64             `json:"baseline_coverage_percent,omitempty"`
	TotalStatements   int                 `json:"total_statements,omitempty"`
	CoveredStatements int                 `json:"covered_statements,omitempty"`
	FinalCoverage     float64             `json:"final_coverage_percent,omitempty"`
	Attempts          int                 `json:"attempts,omitempty"`
	GeneratedFiles    []string            `json:"generated_files,omitempty"`
	TargetOutcomes    []PrepTargetContext `json:"target_outcomes,omitempty"`
	FailureArtifacts  []string            `json:"failure_artifacts,omitempty"`
	AlreadySufficient bool                `json:"already_sufficient,omitempty"`
	Targets           []PrepTarget        `json:"targets,omitempty"`
	HistoryPath       string              `json:"history_path"`
}

// RunPrep executes one guarded prep run: preflight the work tree, run the
// baseline suite with coverage inside an isolated copy, then report an
// already-sufficient result, a ranked worklist, or guarded generated tests.
// The run appends one ledger record.
func RunPrep(ctx context.Context, opts PrepOptions) (PrepReport, error) {
	run := newPrepRun(opts)
	opts = run.opts
	tree, err := CheckWorkTree(ctx, opts.RepoPath)
	if err != nil {
		return run.report, err
	}
	run.report.RepoHead = tree.Head
	if !tree.OK() {
		return run.finish(OutcomePreflight, tree.Reason, TestResult{})
	}
	if lookErr := LookGo(); lookErr != nil {
		return run.report, lookErr
	}
	workspace := preparePrepWorkspace(ctx, opts, tree.Head)
	if workspace.Err != nil {
		return run.report, workspace.Err
	}
	defer func() { _ = workspace.Cleanup() }()
	baseline := runPrepBaseline(ctx, workspace.Dir, workspace.Options)
	if baseline.Err != nil {
		return run.report, baseline.Err
	}
	if baseline.Outcome != "" {
		return run.finish(baseline.Outcome, baseline.Reason, baseline.Result)
	}
	result := baseline.Result
	targets := rankPrepTargets(result.Files, workspace.Options.Floor*100, workspace.Options.MaxTargets, result.TotalStmts)
	if len(targets) == 0 {
		return run.finish(OutcomeAlreadySufficient, fmt.Sprintf("no source file falls below the %.2f%% floor", workspace.Options.Floor*100), result)
	}
	run.report.Targets = targets
	if workspace.Options.Generator != nil {
		generated, genErr := runGeneratedPrep(ctx, generatedPrepInput{Dir: workspace.Dir, SourceHead: tree.Head, Baseline: result, Targets: targets, Options: workspace.Options})
		run.report.Attempts, run.report.GeneratedFiles, run.report.FinalCoverage = generated.Attempts, generated.Files, generated.Final.TotalCoverage
		run.report.TargetOutcomes, run.report.FailureArtifacts = generated.TargetOutcomes, generated.FailureArtifacts
		if genErr != nil {
			return run.report, genErr
		}
		return run.finish(generated.Outcome, generated.Reason, result)
	}
	return run.finish(OutcomePlanned, fmt.Sprintf("ranked %d test-prep target(s) below the %.2f%% coverage floor", len(targets), workspace.Options.Floor*100), result)
}

type prepRun struct {
	opts    PrepOptions
	history *History
	report  PrepReport
}

func newPrepRun(opts PrepOptions) *prepRun {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	history := NewHistory(HistoryPath(opts.StateDir))
	return &prepRun{opts: opts, history: history, report: PrepReport{Schema: PrepSchema, RunType: RunTypePrep, RepoPath: opts.RepoPath, DryRun: !opts.Live, LiveApply: modeDescription(opts.Live), Floor: opts.Floor, FloorPercent: opts.Floor * 100, HistoryPath: history.Path()}}
}

func (r *prepRun) finish(outcome Outcome, reason string, baseline TestResult) (PrepReport, error) {
	r.report.Outcome, r.report.Reason = string(outcome), reason
	r.report.BaselineCoverage, r.report.TotalStatements, r.report.CoveredStatements = baseline.TotalCoverage, baseline.TotalStmts, baseline.CoveredStmts
	if r.report.FinalCoverage == 0 {
		r.report.FinalCoverage = baseline.TotalCoverage
	}
	r.report.Success = outcome == OutcomeAlreadySufficient || outcome == OutcomePlanned || outcome == OutcomeSuccess
	r.report.AlreadySufficient = outcome == OutcomeAlreadySufficient
	record := Record{StrategyID: r.opts.StrategyID, Provider: r.opts.Provider, Model: r.opts.Model, Timestamp: r.opts.Now(), RunType: RunTypePrep, RepoPath: r.opts.RepoPath, RepoHead: r.report.RepoHead, Success: r.report.Success, Outcome: string(outcome), Reason: reason, DryRun: r.report.DryRun, BaselineCoverage: baseline.TotalCoverage, FinalCoverage: r.report.FinalCoverage, AlreadySufficient: r.report.AlreadySufficient, Prep: prepHistoryContext(r.report, baseline.TotalCoverage)}
	if err := r.history.Append(record); err != nil {
		return r.report, fmt.Errorf("record prep history: %w", err)
	}
	return r.report, nil
}

type prepWorkspace struct {
	Dir     string
	Options PrepOptions
	Cleanup func() error
	Err     error
}

type prepBaseline struct {
	Result  TestResult
	Outcome Outcome
	Reason  string
	Err     error
}

func preparePrepWorkspace(ctx context.Context, opts PrepOptions, head string) prepWorkspace {
	if !opts.Live {
		dir, err := IsolateWorkTree(opts.RepoPath)
		if err != nil {
			return prepWorkspace{Err: err}
		}
		return prepWorkspace{Dir: dir, Options: prepScopedGenerator(opts, dir), Cleanup: func() error { removeTree(dir); return nil }}
	}
	dir, cleanup, err := IsolateLinkedWorkTree(ctx, opts.RepoPath, head)
	if err != nil {
		return prepWorkspace{Err: err}
	}
	return prepWorkspace{Dir: dir, Options: prepScopedGenerator(opts, dir), Cleanup: cleanup}
}

func prepScopedGenerator(opts PrepOptions, dir string) PrepOptions {
	if scoped, ok := opts.Generator.(interface {
		WithRepository(string) ProviderTestGenerator
	}); ok {
		opts.Generator = scoped.WithRepository(dir)
	}
	return opts
}

func runPrepBaseline(ctx context.Context, dir string, opts PrepOptions) prepBaseline {
	result, err := (Toolchain{TestTimeout: opts.TestTimeout}).RunTests(ctx, dir)
	if err != nil {
		return prepBaseline{Result: result, Err: fmt.Errorf("baseline suite: %w", err)}
	}
	if !result.AllPassed {
		return prepBaseline{Result: result, Outcome: OutcomeBaselineTests, Reason: "baseline tests not green; refusing to plan prep on a red suite"}
	}
	if !result.ProfileMeasured {
		return prepBaseline{Result: result, Outcome: OutcomeDiff, Reason: "baseline coverage profile was not measured"}
	}
	if result.TotalCoverage >= opts.Floor*100 {
		return prepBaseline{Result: result, Outcome: OutcomeAlreadySufficient, Reason: fmt.Sprintf("baseline coverage %.2f%% already meets the %.2f%% floor", result.TotalCoverage, opts.Floor*100)}
	}
	return prepBaseline{Result: result}
}

func prepHistoryContext(report PrepReport, baseline float64) *PrepContext {
	context := &PrepContext{
		AlreadySufficient: report.AlreadySufficient,
		BaselineCoverage:  baseline,
		FinalCoverage:     report.FinalCoverage,
		CoverageDelta:     report.FinalCoverage - baseline,
		CoverageIncreased: report.FinalCoverage > baseline,
		TestsAdded:        len(report.GeneratedFiles),
	}
	if len(report.TargetOutcomes) > 0 {
		context.Targets = append(context.Targets, report.TargetOutcomes...)
		return context
	}
	for index, target := range report.Targets {
		context.Targets = append(context.Targets, PrepTargetContext{
			RepoRelFile:       target.File,
			Outcome:           report.Outcome,
			WorklistRank:      index + 1,
			CoverageIncreased: context.CoverageIncreased,
			Reason:            report.Reason,
			Score:             target.Score,
			EstimatedLift:     target.EstimatedLiftPercent,
			BeforeCoverage:    target.CoveragePercent,
			CoverageDelta:     context.CoverageDelta,
		})
	}
	return context
}

// rankPrepTargets ranks uncovered production files by coverage deficit
// weighted by uncovered statements (the deterministic core of Pan improvement's
// worklist scoring: higher deficit and bigger reachable gaps sort first),
// with estimated whole-repo lift when every gap in the file is covered.
// The list is capped at maxTargets when positive.
func rankPrepTargets(files []FileCoverage, floorPercent float64, maxTargets, totalStatements int) []PrepTarget {
	var targets []PrepTarget
	for _, file := range files {
		if file.Coverage >= floorPercent || file.UncoveredStatements <= 0 {
			continue
		}
		deficit := 1 - file.Coverage/100
		target := PrepTarget{
			File:                file.File,
			CoveragePercent:     file.Coverage,
			Deficit:             round2(deficit),
			UncoveredStatements: file.UncoveredStatements,
			Gaps:                file.Gaps,
			Score:               round2(deficit * float64(file.UncoveredStatements)),
		}
		if totalStatements > 0 {
			target.EstimatedLiftPercent = round2(100 * float64(file.UncoveredStatements) / float64(totalStatements))
		}
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Score != targets[j].Score {
			return targets[i].Score > targets[j].Score
		}
		return targets[i].File < targets[j].File
	})
	if maxTargets > 0 && len(targets) > maxTargets {
		targets = targets[:maxTargets]
	}
	return targets
}

func removeTree(dir string) { _ = os.RemoveAll(dir) }
