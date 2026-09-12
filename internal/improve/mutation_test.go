package improve

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseMutationScore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want float64
	}{
		{"0.75", 0.75},
		{`{"score":0.8}`, 0.8},
	} {
		got, err := parseMutationScore(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("parseMutationScore(%q) = %v, %v", tc.in, got, err)
		}
	}
	if _, err := parseMutationScore("1.1"); err == nil {
		t.Fatal("expected out-of-range score rejection")
	}
}

func TestRunMutationCommandDoesNotStartCanceledCommand(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	marker := filepath.Join(t.TempDir(), "started")
	cmd := &exec.Cmd{Path: "/bin/sh", Args: []string{"/bin/sh", "-c", "touch " + marker}}
	err := runMutationCommand(ctx, cmd)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runMutationCommand() error = %v, want context cancellation", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("canceled command created marker: stat error = %v", statErr)
	}
}
