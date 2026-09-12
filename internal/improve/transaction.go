package improve

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// BranchVCS is the narrow mutating seam used by live improve workflows.
// Tests inject a fake; no production target is changed until a caller selects
// live mode and every gate has passed.
type BranchVCS interface {
	CreateBranch(context.Context, string) error
	Revert(context.Context, string) error
	Cleanup(context.Context, string) error
	Commit(context.Context, string) error
}

// BranchTx owns an attempt branch after creation. Every return path must call
// RollbackUnlessSettled; settling is allowed only after dry-run rollback or a
// successful live commit.
type BranchTx struct {
	ctx      context.Context
	vcs      BranchVCS
	baseline string
	branch   string
	settled  bool
}

const cleanupTimeout = 30 * time.Second

// BeginBranchTx creates the branch and returns its lifetime guard.
func BeginBranchTx(ctx context.Context, vcs BranchVCS, baseline, branch string) (*BranchTx, error) {
	if err := vcs.CreateBranch(ctx, branch); err != nil {
		return nil, fmt.Errorf("create branch %q: %w", branch, err)
	}
	return &BranchTx{ctx: ctx, vcs: vcs, baseline: baseline, branch: branch}, nil
}

// MarkSettled records an intentional terminal state.
func (tx *BranchTx) MarkSettled() { tx.settled = true }

// RollbackUnlessSettled restores baseline and removes the branch. Both cleanup
// attempts run so callers retain the full failure evidence.
func (tx *BranchTx) RollbackUnlessSettled() error {
	if tx.settled {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(tx.ctx), cleanupTimeout)
	defer cancel()
	return errors.Join(tx.vcs.Revert(cleanupCtx, tx.baseline), tx.vcs.Cleanup(cleanupCtx, tx.branch))
}
