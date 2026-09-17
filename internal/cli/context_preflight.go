package cli

import (
	"github.com/alecthomas/kong"
	repocontext "github.com/dotcommander/pan/internal/repo"
)

// ContextPreflightCmd is `pan context preflight`.
type ContextPreflightCmd struct {
	Target string `arg:"" type:"path" help:"File or directory whose repository context should be resolved."`
}

// Run resolves applicable guidance and the nearest repository purpose signal.
func (c ContextPreflightCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	result, err := repocontext.Preflight(c.Target, root.Repo, root.Standalone)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), result)
}
