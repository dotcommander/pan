package improve

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// GitVCS performs branch operations inside a caller-selected working tree.
// Guarded workflows use it only for their disposable isolated copy.
type GitVCS struct{ Dir string }

// CreateBranch creates and checks out an attempt branch.
func (g GitVCS) CreateBranch(ctx context.Context, branch string) error {
	return gitRun(ctx, g.Dir, "switch", "--create", branch)
}

// Revert restores the snapshot and detaches HEAD so the attempt branch can be
// removed even after a failed apply or test gate.
func (g GitVCS) Revert(ctx context.Context, baseline string) error {
	if err := gitRun(ctx, g.Dir, "reset", "--hard", baseline); err != nil {
		return err
	}
	return gitRun(ctx, g.Dir, "switch", "--detach", baseline)
}

// Cleanup deletes an attempt branch after Revert detached HEAD from it.
func (g GitVCS) Cleanup(ctx context.Context, branch string) error {
	return gitRun(ctx, g.Dir, "branch", "--delete", "--force", branch)
}

// Commit records a successfully gated proposal on the current attempt branch.
func (g GitVCS) Commit(ctx context.Context, message string) error {
	if err := gitRun(ctx, g.Dir, "add", "--all"); err != nil {
		return err
	}
	return gitRun(ctx, g.Dir, "commit", "--message", message)
}

// ResetAttempt clears generated trial files while retaining the attempt branch.
func (g GitVCS) ResetAttempt(ctx context.Context, baseline string) error {
	if err := gitRun(ctx, g.Dir, "reset", "--hard", baseline); err != nil {
		return err
	}
	return gitRun(ctx, g.Dir, "clean", "--force", "-d")
}

// InitIsolatedRepository creates a throwaway repository for guarded branch
// mechanics. It never touches the source work tree copied into dir.
func InitIsolatedRepository(ctx context.Context, dir string) (string, error) {
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "Pan Improve"},
		{"config", "user.email", "pan-improve@localhost"},
		{"add", "--all"},
		{"commit", "--quiet", "--message", "isolated baseline"},
	} {
		if err := gitRun(ctx, dir, args...); err != nil {
			return "", fmt.Errorf("initialize isolated repository: %w", err)
		}
	}
	head, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read isolated baseline: %w", err)
	}
	if head = strings.TrimSpace(head); head == "" {
		return "", errors.New("read isolated baseline: empty HEAD")
	}
	return head, nil
}

func gitRun(ctx context.Context, dir string, args ...string) error {
	_, err := gitOutput(ctx, dir, args...)
	return err
}
