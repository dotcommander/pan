package cli

import (
	"context"
	"errors"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/checks"
)

// ChecksCmd is `pan scan checks`.
type ChecksCmd struct {
	Target string   `arg:"" optional:"" default:"." help:"Repository-relative SKILL.md file or containing directory."`
	Check  []string `name:"check" help:"Run only this built-in check ID (repeatable)."`
	List   bool     `name:"list" help:"List built-in checks without running them."`
}

// Validate rejects selectors that conflict with registry listing.
func (c ChecksCmd) Validate() error {
	if c.List && len(c.Check) != 0 {
		return errors.New("--list cannot be combined with --check")
	}
	return nil
}

// Run executes trusted, in-process repository checks.
func (c ChecksCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	registry := checks.Builtins()
	if c.List {
		return emitResult(kctx, root, deps, displayRepo(root.Repo), registry.List())
	}
	report, err := registry.Run(ctx, checks.Request{Root: root.Repo, Target: c.Target, CheckIDs: c.Check})
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), report)
}
