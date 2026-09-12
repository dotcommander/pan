package app

import (
	"context"
	"time"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/improve"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
	"github.com/dotcommander/pan/internal/scan"
)

func (s Service) improveExcludes(ctx context.Context, root string) []string {
	ignored, err := scan.IgnoredPaths(ctx, root)
	if err != nil {
		ignored = nil
	}
	ignored = append(ignored, ".work")
	return config.NormalizeExcludes(append(s.deps.Config.Exclude, ignored...))
}

// improvePolicy resolves the effective guarded-improvement rules and the
// state directory holding the local run ledger.
func (s Service) improvePolicy() (config.ImproveRules, string, error) {
	rules := s.deps.Config.Improve.Normalized()
	stateDir, err := rules.ImproveStateDir()
	if err != nil {
		return config.ImproveRules{}, "", err
	}
	return rules, stateDir, nil
}

// ImproveRecommend recommends the next improvement task from lexical
// dead-symbol evidence and the local run ledger. Read-only.
func (s Service) ImproveRecommend(ctx context.Context, root string) (improve.RecommendReport, error) {
	return s.ImproveRecommendWithConfig(ctx, root, improveconfig.Default(), "")
}

// ImproveRecommendWithConfig scopes history to the selected stable strategy
// and preserves the optional Improve config in the generated command.
func (s Service) ImproveRecommendWithConfig(ctx context.Context, root string, improveCfg improveconfig.Config, configPath string) (improve.RecommendReport, error) {
	rules, stateDir, err := s.improvePolicy()
	if err != nil {
		return improve.RecommendReport{}, err
	}
	if improveCfg.StateDir != "" {
		stateDir = improveCfg.StateDir
	}
	floor := rules.CoverageFloor
	if improveCfg.CoverageFloor > 0 {
		floor = improveCfg.CoverageFloor
	}
	return improve.RunRecommend(improve.RecommendOptions{
		RepoPath:     root,
		StateDir:     stateDir,
		StrategyID:   selectedStrategy(improveCfg),
		ConfigPath:   configPath,
		Floor:        floor,
		MaxTargets:   rules.MaxWorklistTargets,
		RecentWindow: rules.RecentWindow,
		Exclude:      s.improveExcludes(ctx, root),
	})
}

// ImproveCensus measures the coverage census inside an isolated copy of
// the target work tree. The target is never written.
func (s Service) ImproveCensus(ctx context.Context, root string) (improve.CensusReport, error) {
	return s.ImproveCensusWithTimeout(ctx, root, 0)
}

// ImproveCensusWithTimeout overrides the configured suite bound when positive.
func (s Service) ImproveCensusWithTimeout(ctx context.Context, root string, timeout time.Duration) (improve.CensusReport, error) {
	return s.ImproveCensusWithOptions(ctx, root, "", timeout)
}

// ImproveCensusWithOptions accepts an existing profile when supplied; this
// preserves source census semantics without running a target suite.
func (s Service) ImproveCensusWithOptions(ctx context.Context, root, coverProfile string, timeout time.Duration) (improve.CensusReport, error) {
	rules, _, err := s.improvePolicy()
	if err != nil {
		return improve.CensusReport{}, err
	}
	if timeout <= 0 {
		timeout = rules.TestTimeout
	}
	return improve.RunCensus(ctx, improve.CensusOptions{
		RepoPath:     root,
		CoverProfile: coverProfile,
		TestTimeout:  timeout,
		Exclude:      s.improveExcludes(ctx, root),
	})
}

// ImproveProbe builds and validates the deterministic proposal without
// applying anything anywhere.
func (s Service) ImproveProbe(ctx context.Context, root string) (improve.ProbeReport, error) {
	return s.ImproveProbeWithProvider(ctx, root, "off", ImproveProviderOptions{})
}

// ImproveProbeWithProvider uses a configured provider only when explicitly
// selected; otherwise it preserves the deterministic local proposal path.
func (s Service) ImproveProbeWithProvider(ctx context.Context, root, trace string, providerOpts ImproveProviderOptions) (improve.ProbeReport, error) {
	_, _, err := s.improvePolicy()
	if err != nil {
		return improve.ProbeReport{}, err
	}
	providerOpts.Config.AgentTraceMode = trace
	excludes := s.improveExcludes(ctx, root)
	proposer, err := providerOpts.proposalProposer(root, excludes)
	if err != nil {
		return improve.ProbeReport{}, err
	}
	return improve.RunProbeContext(ctx, improve.ProbeOptions{
		RepoPath: root,
		Exclude:  excludes,
		Proposer: proposer,
		Trace:    trace,
	})
}

// ImprovePrep runs one guarded test-prep planning run: preflight, green
// baseline in an isolated copy, then a ranked worklist or an
// already-sufficient verdict. Dry-run only.
func (s Service) ImprovePrep(ctx context.Context, root string) (improve.PrepReport, error) {
	return s.ImprovePrepWithOptions(ctx, root, false, ImprovePrepOptions{}, ImproveProviderOptions{})
}

// ImprovePrepOptions carries explicit command-level generation bounds.
type ImprovePrepOptions struct {
	Floor             float64
	Retries           int
	RequestTimeout    time.Duration
	RequestInterval   time.Duration
	CorrectiveTimeout time.Duration
	TimeBudget        time.Duration
	ContinueToFloor   bool
	MaxFiles          int
	StrategyID        string
	Provider          string
	Model             string
	TargetRounds      int
	BatchFullSuite    bool
}

// ApplyConfig fills omitted command controls from the isolated Improve config.
// Explicit command values always retain precedence.
func (o *ImprovePrepOptions) ApplyConfig(cfg improveconfig.Config) {
	o.StrategyID = selectedStrategy(cfg)
	o.Provider = cfg.Provider
	o.Model = cfg.Model
	if o.Floor == 0 {
		o.Floor = cfg.CoverageFloor
	}
	if o.Retries == 0 {
		o.Retries = cfg.PrepRetries()
	}
	if o.RequestTimeout == 0 {
		o.RequestTimeout = cfg.PrepTimeout()
	}
	if o.RequestInterval == 0 {
		o.RequestInterval = cfg.RequestInterval
	}
	if o.CorrectiveTimeout == 0 {
		o.CorrectiveTimeout = cfg.CorrectiveTimeout
	}
	if o.TimeBudget == 0 {
		o.TimeBudget = cfg.TimeBudget
	}
	if !o.ContinueToFloor {
		o.ContinueToFloor = cfg.ToFloor
	}
	if o.MaxFiles == 0 {
		o.MaxFiles = cfg.MaxFiles
	}
	o.TargetRounds = cfg.PrepTargetRounds
	o.BatchFullSuite = cfg.PrepBatchFullSuite
}

// ImprovePrepWithOptions enables generated test prep only when a provider is
// explicitly configured. Live settlement is passed through only by its caller.
func (s Service) ImprovePrepWithOptions(ctx context.Context, root string, live bool, command ImprovePrepOptions, providerOpts ImproveProviderOptions) (improve.PrepReport, error) {
	rules, stateDir, err := s.improvePolicy()
	if err != nil {
		return improve.PrepReport{}, err
	}
	rules, stateDir = configuredImprovePolicy(rules, stateDir, providerOpts.Config)
	excludes := s.improveExcludes(ctx, root)
	generator, err := providerOpts.testGenerator(root, excludes)
	if err != nil {
		return improve.PrepReport{}, err
	}
	floor := rules.CoverageFloor
	if command.Floor > 0 {
		floor = command.Floor
	}
	return improve.RunPrep(ctx, improve.PrepOptions{
		RepoPath:          root,
		StateDir:          stateDir,
		Floor:             floor,
		MaxTargets:        rules.MaxWorklistTargets,
		TestTimeout:       rules.TestTimeout,
		Exclude:           excludes,
		Generator:         generator,
		MaxRetries:        command.Retries,
		RequestTimeout:    command.RequestTimeout,
		RequestInterval:   command.RequestInterval,
		CorrectiveTimeout: command.CorrectiveTimeout,
		TimeBudget:        command.TimeBudget,
		ContinueToFloor:   command.ContinueToFloor,
		MaxFiles:          command.MaxFiles,
		TargetRounds:      command.TargetRounds,
		BatchFullSuite:    command.BatchFullSuite,
		MaxConcurrent:     providerOpts.Config.PrepMaxConcurrent,
		Live:              live,
		BranchPrefix:      rules.BranchPrefix,
		StrategyID:        command.StrategyID,
		Provider:          firstNonEmpty(command.Provider, providerOpts.Provider),
		Model:             firstNonEmpty(providerOpts.Model, command.Model),
	})
}

// ImproveRefactor runs the default guarded refactor dry-run in an isolated
// copy. ImproveRefactorWithOptions is the explicit live settlement path.
func (s Service) ImproveRefactor(ctx context.Context, root string) (improve.RefactorReport, error) {
	return s.ImproveRefactorWithOptions(ctx, root, false)
}

// ImproveRefactorWithOptions runs a guarded refactor. Live settlement is an
// explicit caller choice; the default command path remains reviewable dry-run.
func (s Service) ImproveRefactorWithOptions(ctx context.Context, root string, live bool) (improve.RefactorReport, error) {
	return s.ImproveRefactorWithPolicy(ctx, root, RefactorPolicy{Live: live}, ImproveProviderOptions{})
}

// RefactorPolicy holds caller-selected settlement and validation gates.
type RefactorPolicy struct {
	Live           bool
	CoverageGate   bool
	CoverageDrop   float64
	MutationGate   bool
	MutationFloor  float64
	MutationRunner string
	Staticcheck    bool
}

// ImproveRefactorWithPolicy applies explicit gate overrides. A mutation runner
// is started only when both the gate and its executable path are supplied.
func (s Service) ImproveRefactorWithPolicy(ctx context.Context, root string, policy RefactorPolicy, providerOpts ImproveProviderOptions) (improve.RefactorReport, error) {
	rules, stateDir, err := s.improvePolicy()
	if err != nil {
		return improve.RefactorReport{}, err
	}
	rules, stateDir = configuredImprovePolicy(rules, stateDir, providerOpts.Config)
	excludes := s.improveExcludes(ctx, root)
	proposer, err := providerOpts.proposalProposer(root, excludes)
	if err != nil {
		return improve.RefactorReport{}, err
	}
	var runner improve.MutationRunner
	if policy.MutationRunner != "" {
		runner = improve.CommandMutationRunner{Path: policy.MutationRunner}
	}
	return improve.RunRefactor(ctx, improve.RefactorOptions{
		RepoPath:            root,
		MinBaselineCoverage: rules.CoverageFloor,
		StateDir:            stateDir,
		TestTimeout:         rules.TestTimeout,
		BranchPrefix:        rules.BranchPrefix,
		Live:                policy.Live,
		CoverageGate:        policy.CoverageGate,
		CoverageDrop:        policy.CoverageDrop,
		MutationGate:        policy.MutationGate,
		MutationFloor:       policy.MutationFloor,
		MutationRunner:      runner,
		Staticcheck:         policy.Staticcheck || providerOpts.Config.Staticcheck,
		Exclude:             excludes,
		Proposer:            proposer,
		StrategyID:          providerOpts.StrategyID,
		Provider:            providerOpts.Provider,
		Model:               providerOpts.Model,
		FeePerLine:          providerOpts.FeePerLine,
		DeadSymbolsFirst:    providerOpts.Config.DeadSymbolsFirst,
		MaxRetries:          providerOpts.Config.MaxRetries,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
