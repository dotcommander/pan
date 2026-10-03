package improve

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// IsolateLinkedWorkTree records ownership before Git creates the detached tree.
// Successful branches remain for review. Cleanup addresses this exact tree only.
func IsolateLinkedWorkTree(ctx context.Context, repo, baseline string) (string, func() error, error) {
	dir, err := os.MkdirTemp("", "pan-improve-worktree-")
	if err != nil {
		return "", nil, fmt.Errorf("create linked worktree directory: %w", err)
	}
	if err := recordWorktreeOwnership(dir, repo, baseline, true); err != nil {
		_ = os.Remove(dir)
		return "", nil, err
	}
	cleanup := func() error {
		if err := requireOwnedWorktree(dir); err != nil {
			return retainedWorktreeError(dir, err)
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := gitRun(cleanupCtx, repo, "worktree", "remove", "--force", dir); err != nil {
			return retainedWorktreeError(dir, err)
		}
		if err := os.Remove(ownershipPath(dir)); err != nil {
			return retainedWorktreeError(dir, err)
		}
		return nil
	}
	if err := gitRun(ctx, repo, "worktree", "add", "--detach", "--quiet", dir, baseline); err != nil {
		// A failed add may already have registered a worktree. Retain ownership if
		// exact removal cannot establish cleanup; never invoke global prune.
		cleanupErr := cleanup()
		return "", nil, errors.Join(fmt.Errorf("create linked worktree: %w", err), cleanupErr)
	}
	return dir, cleanup, nil
}
