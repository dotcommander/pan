package app

import (
	"context"
	"errors"

	"github.com/dotcommander/pan/internal/clean"
)

// cleanOptions builds normalized clean options from the loaded
// configuration. Omitted flags fall back to configured defaults.
func (s Service) cleanOptions(root string, maxDepth, staleDays *int) clean.Options {
	cfg := s.deps.Config.Normalized()
	options := clean.Options{
		Root:    root,
		Rules:   cfg.Clean,
		Exclude: cfg.Exclude,
	}
	if maxDepth != nil {
		options.MaxDepth = *maxDepth
		options.MaxDepthSet = true
	}
	if staleDays != nil {
		options.StaleDays = *staleDays
		options.StaleDaysSet = true
	}
	return options
}

// CleanPlan runs one read-only cleanup analysis and returns the plan.
func (s Service) CleanPlan(ctx context.Context, root string, maxDepth, staleDays *int) (clean.Plan, error) {
	analysis, err := clean.Analyze(ctx, s.cleanOptions(root, maxDepth, staleDays))
	if err != nil {
		return clean.Plan{}, err
	}
	return analysis.Plan, nil
}

// CleanFindings returns the signal-grouped findings behind a plan.
func (s Service) CleanFindings(ctx context.Context, root string, maxDepth, staleDays *int) (clean.FindingsReport, error) {
	return clean.Findings(ctx, s.cleanOptions(root, maxDepth, staleDays))
}

// CleanMissing returns the repository-completeness gap report.
func (s Service) CleanMissing(ctx context.Context, root string, maxDepth, staleDays *int) (clean.CompletenessReport, error) {
	return clean.Missing(ctx, s.cleanOptions(root, maxDepth, staleDays))
}

// CleanCommands returns the copy-paste command report for the current
// plan. Commands are a review artifact; pan never executes them.
func (s Service) CleanCommands(ctx context.Context, root string, maxDepth, staleDays *int) (clean.CommandsReport, error) {
	opts := s.cleanOptions(root, maxDepth, staleDays)
	analysis, err := clean.Analyze(ctx, opts)
	if err != nil {
		return clean.CommandsReport{}, err
	}
	opts, err = opts.Resolved()
	if err != nil {
		return clean.CommandsReport{}, err
	}
	return clean.BuildCommandsReport(analysis.Plan.Path, clean.BuildActions(analysis.Plan, opts)), nil
}

// CleanApply applies the current plan. Without confirm it is a pure
// dry-run; with confirm it backs up every touched path, executes the
// actions, and writes the manifest beside the backup.
func (s Service) CleanApply(ctx context.Context, root string, maxDepth, staleDays *int, confirm bool) (clean.ApplyResult, error) {
	opts := s.cleanOptions(root, maxDepth, staleDays)
	analysis, err := clean.Analyze(ctx, opts)
	if err != nil {
		return clean.ApplyResult{}, err
	}
	if !analysis.Plan.GitAvailable {
		return clean.ApplyResult{}, errors.New("clean apply blocked: repository state is unknown")
	}
	opts, err = opts.Resolved()
	if err != nil {
		return clean.ApplyResult{}, err
	}
	return clean.Apply(ctx, opts, clean.BuildActions(analysis.Plan, opts), confirm)
}
