package agent

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/review"
)

// blockingBuild delays its first call until the request context ends; later
// builds answer immediately so the same session can continue.
func blockingBuild(blocked *bool, mu *sync.Mutex) ReportBuilder {
	return func(ctx context.Context) (review.Document, error) {
		mu.Lock()
		first := *blocked
		*blocked = false
		mu.Unlock()
		if first {
			<-ctx.Done()
			return review.Document{}, ctx.Err()
		}
		return fixedDocument(), nil
	}
}

func TestRunWithSessionTimeoutAnswersRequestTimeoutAndContinues(t *testing.T) {
	t.Parallel()
	blocked := true
	var mu sync.Mutex
	session := Session{Build: blockingBuild(&blocked, &mu), Timeout: 20 * time.Millisecond}
	input := strings.Join([]string{
		`{"schema":"pan.agent/v1","id":"slow","op":"report"}`,
		`{"schema":"pan.agent/v1","id":"next","op":"report"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := RunWithSession(context.Background(), bufio.NewReader(strings.NewReader(input)), &output, session); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses = %q", output.String())
	}
	if !strings.Contains(lines[0], `"error":{"code":"request_timeout"}`) {
		t.Fatalf("timeout response = %q", lines[0])
	}
	if !strings.Contains(lines[1], `"ok":true`) || !strings.Contains(lines[1], `"id":"next"`) {
		t.Fatalf("session did not continue after timeout: %q", lines[1])
	}
}
