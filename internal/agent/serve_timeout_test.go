package agent

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

// blockingServeBackend delays one named method until its context ends; the
// remaining methods answer immediately.
type blockingServeBackend struct {
	blockMethod string
	blocked     atomic.Bool
}

func (b *blockingServeBackend) AgentSnapshotStatus(ctx context.Context) (SnapshotStatus, error) {
	return SnapshotStatus{}, nil
}

func (b *blockingServeBackend) AgentStatus(ctx context.Context) (StatusSummary, error) {
	if b.hit(ctx, b.blockMethod == MethodStatus) {
		return StatusSummary{}, ctx.Err()
	}
	return StatusSummary{Repository: "/fixture"}, nil
}

func (b *blockingServeBackend) AgentOverview(ctx context.Context) (scan.OverviewReport, error) {
	if b.hit(ctx, b.blockMethod == MethodOverview) {
		return scan.OverviewReport{}, ctx.Err()
	}
	return scan.OverviewReport{}, nil
}

func (b *blockingServeBackend) AgentSymbols(context.Context, string, int) ([]analyze.Symbol, error) {
	return nil, nil
}

func (b *blockingServeBackend) AgentReport(ctx context.Context) (review.Document, error) {
	return review.Document{}, nil
}

func (b *blockingServeBackend) AgentMapRender(context.Context, string) (string, error) {
	return "map", nil
}

func (b *blockingServeBackend) AgentMapStatus(ctx context.Context) (MapStatus, error) {
	return MapStatus{Root: "/fixture"}, nil
}

func (b *blockingServeBackend) AgentSymbolFind(context.Context, string) (any, error) {
	return []string{"match"}, nil
}

func (b *blockingServeBackend) AgentFileExplain(context.Context, string) (any, error) {
	return nil, nil
}

func (b *blockingServeBackend) AgentFileContext(context.Context, string, string, string, int) (any, error) {
	return nil, nil
}

// hit reports whether this call should block until ctx ends.
func (b *blockingServeBackend) hit(ctx context.Context, match bool) bool {
	if !match || !b.blocked.CompareAndSwap(false, true) {
		return false
	}
	<-ctx.Done()
	return true
}

func TestRunServeTimeoutAnswersErrorAndKeepsSession(t *testing.T) {
	t.Parallel()
	backend := &blockingServeBackend{blockMethod: MethodStatus}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"slow","method":"pan/status"}`,
		`{"jsonrpc":"2.0","id":"next","method":"pan/status"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	err := RunServeWithTimeout(context.Background(), bufio.NewReader(strings.NewReader(input)), &output, backend, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses = %q", output.String())
	}
	if !strings.Contains(lines[0], `"error"`) || !strings.Contains(lines[0], messageRequestTimeout) {
		t.Fatalf("timeout response = %q", lines[0])
	}
	if !strings.Contains(lines[0], `"id":"slow"`) {
		t.Fatalf("timeout response lost id: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"result"`) || !strings.Contains(lines[1], `"id":"next"`) {
		t.Fatalf("session did not continue after timeout: %q", lines[1])
	}
}
