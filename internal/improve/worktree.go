package improve

import (
	"context"
	"fmt"
	"os"
)

// IsolateLinkedWorkTree creates a detached linked worktree at baseline. Live
// attempts run there, so a proposal, rollback, or commit never resets or adds
// files in the caller's checkout. A successful attempt branch remains after
// cleanup for explicit review or merge.
func IsolateLinkedWorkTree(ctx context.Context, repo, baseline string) (string, func() error, error) {
	dir, err := os.MkdirTemp("", "pan-improve-worktree-")
	if err != nil {
		return "", nil, fmt.Errorf("create linked worktree directory: %w", err)
	}
	if err := gitRun(ctx, repo, "worktree", "add", "--detach", "--quiet", dir, baseline); err != nil {
		_ = os.Remove(dir)
		return "", nil, fmt.Errorf("create linked worktree: %w", err)
	}
	cleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := gitRun(cleanupCtx, repo, "worktree", "remove", "--force", dir); err != nil {
			return fmt.Errorf("remove linked worktree: %w", err)
		}
		return nil
	}
	return dir, cleanup, nil
}
