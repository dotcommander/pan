package cli

import (
	"context"
	"errors"

	"github.com/alecthomas/kong"
)

// CleanCmd groups cleanup planning and application. Plan, findings,
// missing, and commands are strictly read-only; apply is a dry-run until
// --confirm, and a confirmed apply backs up every touched path and writes a
// manifest before changing anything.
type CleanCmd struct {
	Plan     CleanPlanCmd     `cmd:"" help:"Cleanup plan: candidates, health score, summary."`
	Findings CleanFindingsCmd `cmd:"" help:"Cleanup findings grouped by signal."`
	Missing  CleanMissingCmd  `cmd:"" help:"Repository completeness gaps."`
	Commands CleanCommandsCmd `cmd:"" help:"Copy-paste cleanup commands (never executed)."`
	Apply    CleanApplyCmd    `cmd:"" help:"Apply the cleanup plan; dry-run unless --confirm."`
}

// cleanScope carries the flags shared by every clean leaf. Omitted flags
// select the configured defaults.
type cleanScope struct {
	MaxDepth  *int `name:"max-depth" help:"Maximum directory depth; omit to use the configured default."`
	StaleDays *int `name:"stale-days" help:"Days before tracked files count as stale; omit to use the configured default."`
}

func (s cleanScope) validate() error {
	if s.MaxDepth != nil && *s.MaxDepth < 0 {
		return errors.New("--max-depth must not be negative")
	}
	if s.StaleDays != nil && *s.StaleDays < 0 {
		return errors.New("--stale-days must not be negative")
	}
	return nil
}

// CleanPlanCmd is `pan clean plan`.
type CleanPlanCmd struct {
	cleanScope
	Detail detailLevel `name:"detail" help:"Detail level: compact (default) or evidence." default:"compact"`
}

// Validate rejects negative scope flags and invalid detail levels.
func (c CleanPlanCmd) Validate() error {
	if err := c.validate(); err != nil {
		return err
	}
	return validateCleanDetail(c.Detail)
}

// Run executes `pan clean plan`.
func (c CleanPlanCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	plan, err := deps.App.CleanPlan(ctx, root.Repo, c.MaxDepth, c.StaleDays)
	if err != nil {
		return err
	}
	projected := projectCleanPlan(plan, c.Detail)
	return emitResult(kctx, root, deps, plan.Path, projected)
}

// CleanFindingsCmd is `pan clean findings`.
type CleanFindingsCmd struct{ cleanScope }

// Validate rejects negative scope flags.
func (c CleanFindingsCmd) Validate() error { return c.validate() }

// Run executes `pan clean findings`.
func (c CleanFindingsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	report, err := deps.App.CleanFindings(ctx, root.Repo, c.MaxDepth, c.StaleDays)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.Path, report)
}

// CleanMissingCmd is `pan clean missing`.
type CleanMissingCmd struct{ cleanScope }

// Validate rejects negative scope flags.
func (c CleanMissingCmd) Validate() error { return c.validate() }

// Run executes `pan clean missing`.
func (c CleanMissingCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	report, err := deps.App.CleanMissing(ctx, root.Repo, c.MaxDepth, c.StaleDays)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.Path, report)
}

// CleanCommandsCmd is `pan clean commands`.
type CleanCommandsCmd struct{ cleanScope }

// Validate rejects negative scope flags.
func (c CleanCommandsCmd) Validate() error { return c.validate() }

// Run executes `pan clean commands`.
func (c CleanCommandsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	report, err := deps.App.CleanCommands(ctx, root.Repo, c.MaxDepth, c.StaleDays)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, report.Path, report)
}

// CleanApplyCmd is `pan clean apply`. Without --confirm it plans and
// reports without touching the repository. With --confirm it creates a
// tar.gz backup of every path it will touch, applies the actions, and
// writes a JSON manifest beside the backup. Untracked paths only ever move
// on the filesystem; git index operations are reserved for tracked files,
// and no shell is ever invoked.
type CleanApplyCmd struct {
	cleanScope
	Confirm bool `name:"confirm" help:"Back up, write a manifest, and execute the plan."`
}

// Validate rejects negative scope flags.
func (c CleanApplyCmd) Validate() error { return c.validate() }

// Run executes `pan clean apply`.
func (c CleanApplyCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	result, err := deps.App.CleanApply(ctx, root.Repo, c.MaxDepth, c.StaleDays, c.Confirm)
	if err != nil {
		// A partial failure still emits its envelope so every action
		// outcome is visible; the exit code carries the failure.
		if emitErr := emitResult(kctx, root, deps, result.Path, result); emitErr != nil {
			return emitErr
		}
		return &ExitError{Code: ExitFailure, Err: err}
	}
	return emitResult(kctx, root, deps, result.Path, result)
}
