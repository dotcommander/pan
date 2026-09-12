package cli

import (
	"context"
	"testing"
	"time"
)

func TestCommandContextLongLivedCommandsPreserveParentCancellation(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	ctx, cancel := commandContext(parent, []string{"flow", "serve"}, time.Millisecond)
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("flow serve received a command deadline")
	}
	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("flow serve context did not preserve parent cancellation")
	}
}

func TestCommandContextFiniteCommandHonorsParentBound(t *testing.T) {
	t.Parallel()

	wantDeadline := time.Now().Add(time.Hour)
	parent, cancelParent := context.WithDeadline(context.Background(), wantDeadline)
	defer cancelParent()
	ctx, cancel := commandContext(parent, []string{"scan", "overview"}, 2*time.Hour)
	defer cancel()

	gotDeadline, ok := ctx.Deadline()
	if !ok || !gotDeadline.Equal(wantDeadline) {
		t.Fatalf("deadline = %v, want parent deadline %v", gotDeadline, wantDeadline)
	}
	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("finite command context did not preserve parent cancellation")
	}
}

func TestCommandContextFiniteCommandAddsConfiguredDeadline(t *testing.T) {
	t.Parallel()

	const timeout = time.Minute
	before := time.Now()
	ctx, cancel := commandContext(context.Background(), []string{"scan", "overview"}, timeout)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("finite command has no configured deadline")
	}
	if got := deadline.Sub(before); got < timeout-time.Second || got > timeout+time.Second {
		t.Fatalf("deadline offset = %s, want about %s", got, timeout)
	}
}
