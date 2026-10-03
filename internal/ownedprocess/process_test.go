package ownedprocess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func fixture(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessFixture$")
	// The race runtime sleeps for one second at os.Exit by default. Fixtures
	// own no asynchronous teardown; disable that instrumentation delay so the
	// deadline measures process/pipe ownership rather than race exit latency.
	raceOptions := []string{}
	for _, option := range strings.Fields(os.Getenv("GORACE")) {
		if !strings.HasPrefix(option, "atexit_sleep_ms=") {
			raceOptions = append(raceOptions, option)
		}
	}
	raceOptions = append(raceOptions, "atexit_sleep_ms=0")
	cmd.Env = append(os.Environ(), "PAN_PROCESS_FIXTURE="+mode, "GORACE="+strings.Join(raceOptions, " "))
	return cmd
}
func TestProcessFixture(t *testing.T) {
	t.Parallel()
	switch os.Getenv("PAN_PROCESS_FIXTURE") {
	case "stdout":
		fmt.Print(strings.Repeat("x", 8192))
		os.Exit(0)
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 8192))
		os.Exit(0)
	case "block":
		<-time.After(time.Hour)
		os.Exit(0)
	case "descendant":
		cmd := fixture("block")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Print(cmd.Process.Pid)
		os.Exit(0)
	}
}
func TestStdoutOverflowTerminates(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	result, err := Run(ctx, fixture("stdout"), Limits{StdoutBytes: 64, StderrBytes: 64})
	if !errors.Is(err, ErrOutputLimit) || len(result.Stdout) != 64 {
		t.Fatalf("bytes=%d err=%v", len(result.Stdout), err)
	}
}
func TestStderrIsDrainedAndMarked(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	result, err := Run(ctx, fixture("stderr"), Limits{StdoutBytes: 64, StderrBytes: 64})
	if err != nil || !result.StderrTruncated || len(result.Stderr) != 64 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestCancelledOwnedProcess(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)
	_, err := Run(ctx, fixture("block"), Limits{StdoutBytes: 64, StderrBytes: 64})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
func TestDescendantHoldingPipeIsBounded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	_, err := Run(ctx, fixture("descendant"), Limits{StdoutBytes: 64, StderrBytes: 64})
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected inherited pipe timeout: %v", err)
	}
}
