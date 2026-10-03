package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/gitoutgoing"
	"github.com/dotcommander/pan/internal/gitworktree"
	"github.com/dotcommander/pan/internal/render"
)

// GitScanCmd groups bounded, read-only Git evidence commands.
type GitScanCmd struct {
	Worktree WorktreeScanCmd `cmd:"" help:"Inspect local worktree, branch, range, and risk evidence."`
	Outgoing OutgoingScanCmd `cmd:"" help:"Inspect blobs and generic sensitive paths in outgoing revisions."`
}

// WorktreeScanCmd is `pan scan git worktree`.
type WorktreeScanCmd struct {
	Base           string `name:"base" help:"Local base ref for committed-range evidence."`
	MaxItems       int    `name:"max-items" default:"80" help:"Maximum entries retained per bounded list."`
	LargeFileBytes int64  `name:"large-file-bytes" default:"1048576" help:"Threshold for large-file evidence."`
}

func (c WorktreeScanCmd) Validate() error {
	if c.MaxItems <= 0 {
		return errors.New("--max-items must be positive")
	}
	if c.LargeFileBytes < 0 {
		return errors.New("--large-file-bytes must not be negative")
	}
	return nil
}

func (c WorktreeScanCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	report, code := gitworktree.BuildContext(ctx, gitworktree.Options{
		Path: root.Repo, Base: c.Base, MaxItems: c.MaxItems, LargeFileBytes: c.LargeFileBytes,
	}, deps.App.EffectiveConfig().Config.OutgoingGit)
	if code != 0 {
		return fmt.Errorf("git worktree scan failed: %v", report["error"])
	}
	repository, _ := report["repository_root"].(string)
	complete := true
	var limits []string
	if truncated, _ := report["risk_inventory_truncated"].(bool); truncated {
		complete = false
		limits = append(limits, "worktree evidence truncated by --max-items")
	}
	return emitGitResult(kctx, root, deps, repository, report, complete, limits)
}

// OutgoingScanCmd is `pan scan git outgoing`.
type OutgoingScanCmd struct {
	Revision     string `name:"revision" required:"" help:"Outgoing Git revision or range."`
	MaxBlobBytes int64  `name:"max-blob-bytes" default:"1048576" help:"Maximum outgoing blob size."`
	Enforce      bool   `name:"enforce" help:"Emit pre-push diagnostics and fail when findings block the push."`
}

func (c OutgoingScanCmd) Validate() error {
	if c.MaxBlobBytes < 0 {
		return errors.New("--max-blob-bytes must not be negative")
	}
	return nil
}

func (c OutgoingScanCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	report, err := gitoutgoing.InspectContext(ctx, c.Revision, root.Repo, c.MaxBlobBytes, deps.App.EffectiveConfig().Config.OutgoingGit)
	if err != nil {
		return fmt.Errorf("inspect outgoing content: %w", err)
	}
	if c.Enforce {
		if report.PathsTruncated {
			return &ExitError{Code: ExitFailure, Err: errors.New("pre-push: BLOCKED: outgoing path evidence was truncated")}
		}
		if gitoutgoing.Emit(report.Findings, c.MaxBlobBytes, deps.Out) {
			return &ExitError{Code: ExitFailure, Err: errors.New("pre-push: BLOCKED: outgoing content policy failed")}
		}
		return nil
	}
	complete := !report.PathsTruncated
	var limits []string
	if report.PathsTruncated {
		limits = append(limits, "outgoing path evidence truncated at 10000 entries")
	}
	return emitGitResult(kctx, root, deps, root.Repo, report, complete, limits)
}

func emitGitResult(kctx *kong.Context, root *Root, deps Deps, repository string, result any, complete bool, limits []string) error {
	envelope := render.NewEnvelope(commandPath(kctx), repository, analyze.Status{Complete: complete, Limits: limits}, result, nil)
	return render.Write(deps.Out, root.Format, envelope)
}
