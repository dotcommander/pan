package improve

import (
	"context"
	"testing"
)

type fakeBranchVCS struct{ created, reverted, cleaned bool }

func (f *fakeBranchVCS) CreateBranch(context.Context, string) error { f.created = true; return nil }
func (f *fakeBranchVCS) Revert(context.Context, string) error       { f.reverted = true; return nil }
func (f *fakeBranchVCS) Cleanup(context.Context, string) error      { f.cleaned = true; return nil }
func (f *fakeBranchVCS) Commit(context.Context, string) error       { return nil }

func TestBranchTxRollsBackUnlessSettled(t *testing.T) {
	t.Parallel()
	vcs := &fakeBranchVCS{}
	tx, err := BeginBranchTx(context.Background(), vcs, "base", "attempt")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.RollbackUnlessSettled(); err != nil {
		t.Fatal(err)
	}
	if !vcs.created || !vcs.reverted || !vcs.cleaned {
		t.Fatalf("lifecycle = %+v", vcs)
	}
}

func TestBranchTxSettledDoesNotRollback(t *testing.T) {
	t.Parallel()
	vcs := &fakeBranchVCS{}
	tx, err := BeginBranchTx(context.Background(), vcs, "base", "attempt")
	if err != nil {
		t.Fatal(err)
	}
	tx.MarkSettled()
	if err := tx.RollbackUnlessSettled(); err != nil {
		t.Fatal(err)
	}
	if vcs.reverted || vcs.cleaned {
		t.Fatalf("settled tx rolled back: %+v", vcs)
	}
}

func TestBranchTxRollsBackAfterRunContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	vcs := &fakeBranchVCS{}
	tx, err := BeginBranchTx(ctx, vcs, "base", "attempt")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := tx.RollbackUnlessSettled(); err != nil {
		t.Fatal(err)
	}
	if !vcs.reverted || !vcs.cleaned {
		t.Fatalf("canceled cleanup = %+v", vcs)
	}
}
