package cli

import (
	"github.com/alecthomas/kong"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

func suppliedFlag(ctx *kong.Context, name string) bool {
	if ctx == nil {
		return false
	}
	for _, path := range ctx.Path {
		if path.Flag != nil && path.Flag.Name == name && !path.Resolved {
			return true
		}
	}
	return false
}

// prepConfig resolves explicit zero/false flags before strategy identity and
// execution are derived from the same effective settings.
func (c ImprovePrepCmd) prepConfig(ctx *kong.Context, cfg improveconfig.Config) improveconfig.Config {
	if suppliedFlag(ctx, "floor") {
		cfg.CoverageFloor = c.Floor
	}
	if suppliedFlag(ctx, "retries") {
		cfg.PrepTargetRetries = &c.Retries
	}
	if suppliedFlag(ctx, "request-timeout") {
		cfg.PrepRequestTimeout = &c.RequestTimeout
	}
	if suppliedFlag(ctx, "request-interval") {
		cfg.RequestInterval = c.RequestInterval
	}
	if suppliedFlag(ctx, "corrective-timeout") {
		cfg.CorrectiveTimeout = c.CorrectiveTimeout
	}
	if suppliedFlag(ctx, "time-budget") {
		cfg.TimeBudget = c.TimeBudget
	}
	if suppliedFlag(ctx, "to-floor") {
		cfg.ToFloor = c.ToFloor
	}
	if suppliedFlag(ctx, "max-files") {
		cfg.MaxFiles = c.MaxFiles
	}
	return cfg
}

func (c ImproveRefactorCmd) refactorConfig(ctx *kong.Context, cfg improveconfig.Config) improveconfig.Config {
	if suppliedFlag(ctx, "coverage-gate") {
		cfg.CoverageGate = c.CoverageGate
	}
	if suppliedFlag(ctx, "coverage-drop") {
		cfg.CoverageDrop = c.CoverageDrop
	}
	if suppliedFlag(ctx, "mutation-gate") {
		cfg.MutationGate = c.MutationGate
	}
	if suppliedFlag(ctx, "mutation-floor") {
		cfg.MutationFloor = c.MutationFloor
	}
	if suppliedFlag(ctx, "trace-mode") {
		cfg.AgentTraceMode = c.TraceMode
	}
	return cfg
}
