package cli

import (
	"context"

	"github.com/alecthomas/kong"
)

// CacheCmd groups incremental-analysis cache lifecycle commands: status
// inspects without building, warm builds and stores one bounded pass, and
// clear removes the pan-owned cache entry for the repository.
type CacheCmd struct {
	Status CacheStatusCmd `cmd:"" help:"Cache freshness report."`
	Warm   CacheWarmCmd   `cmd:"" help:"Build and store a fresh analysis cache."`
	Clear  CacheClearCmd  `cmd:"" help:"Remove cached analysis state."`
}

// cacheDir selects where cache commands read and write pan-owned state.
type cacheDir struct {
	CacheDir string `name:"cache-dir" help:"Cache root directory (default: user cache directory plus pan)."`
}

// CacheStatusCmd is `pan cache status`: freshness and usability of the
// stored analysis cache for the target repository.
type CacheStatusCmd struct{ cacheDir }

// Run executes `pan cache status`.
func (c CacheStatusCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	status, err := deps.App.CacheStatus(ctx, root.Repo, c.CacheDir)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), status)
}

// CacheWarmCmd is `pan cache warm`: build one bounded analysis pass and
// store it; the stored entry must end up fresh and usable or the command
// fails.
type CacheWarmCmd struct{ cacheDir }

// Run executes `pan cache warm`.
func (c CacheWarmCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, status, err := deps.App.CacheWarm(ctx, root.Repo, c.CacheDir)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, status)
}

// CacheClearCmd is `pan cache clear`: remove the pan-owned cache entry for
// the target repository. It touches only derived cache state, never the
// repository itself.
type CacheClearCmd struct{ cacheDir }

// Run executes `pan cache clear`.
func (c CacheClearCmd) Run(kctx *kong.Context, root *Root, deps Deps, _ context.Context) error {
	result, err := deps.App.CacheClear(root.Repo, c.CacheDir)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), result)
}
