package gitoutgoing

import (
	"bufio"
	"context"
	"errors"
	"github.com/dotcommander/pan/internal/config"
	"strings"
	"testing"
)

func TestInspectCallerCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := InspectContext(ctx, "HEAD", t.TempDir(), 1048576, config.OutgoingGitRules{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected caller cancellation: %v", err)
	}
}
func TestBatchIdentityAndSizes(t *testing.T) {
	t.Parallel()
	oid := strings.Repeat("a", 40)
	for _, header := range []string{strings.Repeat("b", 40) + " blob 10", oid + " blob -1", oid + " tree 1", oid + " blob 9223372036854775808"} {
		if _, ok := blobSizeFromHeader(oid, header); ok {
			t.Fatalf("accepted malformed header %s", header)
		}
	}
}
func TestOversizedBatchHeader(t *testing.T) {
	t.Parallel()
	batch := catFile{ctx: context.Background(), stdin: nopWriteCloser{}, reader: bufio.NewReaderSize(strings.NewReader(strings.Repeat("a", 4096)+"\n"), 4096), headerLimit: 4096}
	if _, err := batch.request(strings.Repeat("a", 40)); err == nil {
		t.Fatal("oversized header accepted")
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
func TestRetainedStderrDrains(t *testing.T) {
	t.Parallel()
	writer := retainedStderr{limit: 4}
	n, err := writer.Write([]byte("123456789"))
	if err != nil || n != 9 || string(writer.data) != "1234" || !writer.truncated {
		t.Fatal("stderr capture failed")
	}
}
