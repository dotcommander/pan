package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/app"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

// ImproveCmd groups pan's guarded improvement workflows, mined from the
// Pan improvement refactoring agent. Commands are local unless provider configuration
// or live mode is supplied. recommend and stats are
// read-only; dry-run prep and refactor use an isolated temporary copy.
type ImproveCmd struct {
	Recommend ImproveRecommendCmd `cmd:"" help:"Recommend the next improvement task from dead-code and history evidence."`
	Census    ImproveCensusCmd    `cmd:"" help:"Coverage census measured inside an isolated copy of the target."`
	Probe     ImproveProbeCmd     `cmd:"" help:"Proposal-only dead-code probe; validates without applying."`
	Prep      ImprovePrepCmd      `cmd:"" help:"Rank or generate guarded tests below the coverage floor."`
	Refactor  ImproveRefactorCmd  `cmd:"" help:"Guarded dead-code refactor in an isolated copy by default."`
	Stats     ImproveStatsCmd     `cmd:"" help:"Outcome statistics from the local run-history ledger."`
	Export    ImproveExportCmd    `cmd:"" help:"Export bounded refactor observations as JSONL."`
}

type ImproveExportCmd struct {
	Output string `name:"output" type:"path" required:"" help:"Write observation JSONL to this path."`
	Limit  int    `name:"limit" default:"100" help:"Maximum records to export (hard limit 10000)."`
	Since  string `name:"since" help:"Include records at or after this RFC3339 time."`
	improveConfigFlags
}

func (c ImproveExportCmd) Run(_ *kong.Context, _ *Root, deps Deps, ctx context.Context) error {
	cfg, err := c.loadImproveConfig()
	if err != nil {
		return err
	}
	var since *time.Time
	if c.Since != "" {
		parsed, parseErr := time.Parse(time.RFC3339, c.Since)
		if parseErr != nil {
			return fmt.Errorf("parse --since: %w", parseErr)
		}
		since = &parsed
	}
	_, err = deps.App.ImproveExport(ctx, c.Output, c.Limit, since, cfg)
	return err
}

// ImproveRecommendCmd is `pan improve recommend`: the deterministic
// next-task recommendation. Read-only.
type ImproveRecommendCmd struct{ improveConfigFlags }

// Run executes `pan improve recommend`.
func (c ImproveRecommendCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	cfg, err := c.loadImproveConfig()
	if err != nil {
		return err
	}
	report, err := deps.App.ImproveRecommendWithConfig(ctx, root.Repo, cfg, c.Config)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.RepoPath, report)
}

// ImproveCensusCmd is `pan improve census`: coverage census measured
// inside an isolated copy of the target work tree.
type ImproveCensusCmd struct {
	CoverProfile string        `name:"coverprofile" type:"path" help:"Read an existing Go coverprofile instead of running the suite."`
	Timeout      time.Duration `help:"Bound the isolated test suite."`
	improveConfigFlags
}

// Run executes `pan improve census`.
func (c ImproveCensusCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	cfg, err := c.loadImproveConfig()
	if err != nil {
		return err
	}
	timeout := c.Timeout
	if !suppliedFlag(kctx, "timeout") {
		timeout = cfg.TestTimeout
	}
	report, err := deps.App.ImproveCensusWithOptions(ctx, root.Repo, c.CoverProfile, timeout)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, root.Repo, report)
}

// ImproveProbeCmd is `pan improve probe`: build and validate a local or
// explicitly provider-backed proposal without applying anything anywhere.
type ImproveProbeCmd struct {
	Trace string `default:"off" enum:"off,summary,full" help:"Report trace verbosity for the proposal response."`
	improveConfigFlags
	providerFlags
}

// Run executes `pan improve probe`.
func (c ImproveProbeCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	cfg, err := c.loadImproveConfig()
	if err != nil {
		return err
	}
	trace := cfg.AgentTraceMode
	if suppliedFlag(kctx, "trace") || trace == "" {
		trace = c.Trace
	}
	report, err := deps.App.ImproveProbeWithProvider(ctx, root.Repo, trace, c.providerOptions(kctx, cfg))
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.RepoPath, report)
}

// ImprovePrepCmd is `pan improve prep`: one guarded test-prep planning
// run. Gate refusals (dirty tree, red baseline, already-sufficient
// coverage) are completed runs with success=false, not command errors.
type ImprovePrepCmd struct {
	Live              bool          `help:"Commit validated tests that increase coverage."`
	DryRun            bool          `help:"Force isolated rollback; conflicts with --live."`
	Floor             float64       `help:"Coverage floor override as a fraction in (0,1]."`
	Retries           int           `help:"Corrective provider retries per coverage target."`
	RequestTimeout    time.Duration `help:"Bound each initial provider request."`
	RequestInterval   time.Duration `help:"Minimum delay between provider request starts."`
	CorrectiveTimeout time.Duration `help:"Bound provider requests after corrective feedback."`
	TimeBudget        time.Duration `help:"Bound the complete generated prep run."`
	ToFloor           bool          `help:"Continue accepting coverage-improving tests until the floor is reached."`
	MaxFiles          int           `help:"Maximum generated test files; zero is unlimited."`
	improveConfigFlags
	providerFlags
}

// Run executes `pan improve prep`.
func (c ImprovePrepCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	if c.Live && c.DryRun {
		return errors.New("--live and --dry-run cannot be used together")
	}
	cfg, err := c.loadImproveConfig()
	if err != nil {
		return err
	}
	cfg = c.prepConfig(kctx, cfg)
	if validateErr := cfg.Validate(); validateErr != nil {
		return validateErr
	}
	providerOpts := c.providerOptions(kctx, cfg)
	var options app.ImprovePrepOptions
	options.ApplyConfig(providerOpts.Config)
	report, err := deps.App.ImprovePrepWithOptions(ctx, root.Repo, c.Live, options, providerOpts)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.RepoPath, report)
}

// ImproveRefactorCmd is `pan improve refactor`: one guarded refactor
// dry-run. Every dry-run mutation lands in an isolated copy; live mode writes
// only after every gate and a successful commit. Gate refusals are completed runs
// with success=false, not command errors.
type ImproveRefactorCmd struct {
	Live           bool    `help:"Commit the fully gated proposal on its attempt branch."`
	DryRun         bool    `help:"Force isolated rollback; conflicts with --live."`
	DeadCode       bool    `name:"deadcode" help:"Force the deterministic dead-code proposal lane."`
	TraceMode      string  `default:"off" enum:"off,summary,full" help:"Record the requested provider trace mode for this refactor."`
	CoverageGate   bool    `help:"Reject changed-file coverage regressions."`
	CoverageDrop   float64 `help:"Maximum allowed changed-file coverage drop in percent."`
	MutationGate   bool    `help:"Require a mutation score from --mutation-runner."`
	MutationFloor  float64 `help:"Minimum mutation score as a fraction in [0,1]."`
	MutationRunner string  `help:"Executable mutation runner; it receives --repo and --packages."`
	improveConfigFlags
	providerFlags
}

// Run executes `pan improve refactor`.
func (c ImproveRefactorCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	if c.Live && c.DryRun {
		return errors.New("--live and --dry-run cannot be used together")
	}
	cfg, err := c.loadImproveConfig()
	if err != nil {
		return err
	}
	cfg = c.refactorConfig(kctx, cfg)
	if validateErr := cfg.Validate(); validateErr != nil {
		return validateErr
	}
	providerOpts := c.providerOptions(kctx, cfg)
	if c.DeadCode {
		providerOpts.Deterministic = true
	}
	policy := app.RefactorPolicy{Live: c.Live, CoverageGate: cfg.CoverageGate, CoverageDrop: cfg.CoverageDrop, MutationGate: cfg.MutationGate, MutationFloor: cfg.MutationFloor, MutationRunner: c.MutationRunner}
	report, err := deps.App.ImproveRefactorWithPolicy(ctx, root.Repo, policy, providerOpts)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.RepoPath, report)
}

// ImproveStatsCmd is `pan improve stats`: deterministic outcome summary
// of the local run-history ledger for the target repository. Read-only.
type ImproveStatsCmd struct {
	Recent   int  `help:"Number of recent history records to include."`
	AllRepos bool `help:"Include ledger records from all repositories."`
	improveConfigFlags
}

// Run executes `pan improve stats`.
func (c ImproveStatsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	cfg, err := c.loadImproveConfig()
	if err != nil {
		return err
	}
	report, err := deps.App.ImproveStatsWithConfig(ctx, root.Repo, c.Recent, c.AllRepos, cfg)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.RepoPath, report)
}

// providerFlags is shared by the optional provider-powered improve commands.
// A configured provider or explicit base URL selects provider-backed proposals.
type providerFlags struct {
	ProviderBaseURL    string        `name:"provider-base-url" help:"OpenAI-compatible provider base URL."`
	ProviderAPIKeyEnv  string        `name:"provider-api-key-env" help:"Environment variable holding the provider API key."`
	ProviderAuthHeader string        `name:"provider-auth-header" help:"Complete Authorization header value; overrides API-key bearer auth."`
	ProviderModel      string        `name:"provider-model" help:"Provider model name."`
	ProviderTimeout    time.Duration `name:"provider-timeout" help:"Bound one provider request."`
}

func (f providerFlags) providerOptions(ctx *kong.Context, cfg improveconfig.Config) app.ImproveProviderOptions {
	if f.ProviderBaseURL != "" {
		cfg.BaseURL = f.ProviderBaseURL
		if cfg.Provider == "" {
			cfg.Provider = "openai-compatible"
		}
	}
	if f.ProviderAPIKeyEnv != "" {
		cfg.APIKeyEnv = f.ProviderAPIKeyEnv
	}
	if f.ProviderAuthHeader != "" {
		cfg.AuthHeader = f.ProviderAuthHeader
	}
	if f.ProviderModel != "" {
		cfg.Model = f.ProviderModel
	}
	if suppliedFlag(ctx, "provider-timeout") || f.ProviderTimeout > 0 {
		cfg.RequestTimeout = f.ProviderTimeout
	}
	strategy := ""
	if cfg.Explicit {
		strategy = cfg.StrategyID()
	}
	return app.ImproveProviderOptions{BaseURL: cfg.BaseURL, APIKeyEnv: cfg.APIKeyEnv, AuthHeader: cfg.AuthHeader, Model: cfg.Model, Timeout: cfg.RequestTimeout, StrategyID: strategy, Provider: cfg.Provider, FeePerLine: cfg.FeePerLine, Config: cfg}
}

// improveConfigFlags loads the isolated Improve configuration. It never reads
// Pan's global analysis configuration and does not contain a raw API key.
type improveConfigFlags struct {
	Config string `name:"config" type:"path" help:"Improve-only YAML settings, including provider environment-variable names."`
}

func (f improveConfigFlags) loadImproveConfig() (improveconfig.Config, error) {
	return improveconfig.Load(f.Config)
}
