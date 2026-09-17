package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	repocontext "github.com/dotcommander/pan/internal/repo"
)

// ContextPreflightCmd is `pan context preflight`.
type ContextPreflightCmd struct {
	// Target intentionally stays a raw string: repo.Preflight resolves
	// relative paths under --repo, matching the documented contract, while
	// kong's path mapper would absolutize them against the working directory.
	Target string `arg:"" help:"File or directory whose repository context should be resolved; relative paths resolve under --repo."`
}

// Run resolves applicable guidance and the nearest repository purpose signal.
func (c ContextPreflightCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	result, err := repocontext.Preflight(expandHome(c.Target), root.Repo, root.Standalone)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), result)
}

// expandHome expands a leading ~ without absolutizing, so repo.Preflight
// keeps ownership of root-relative resolution.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
