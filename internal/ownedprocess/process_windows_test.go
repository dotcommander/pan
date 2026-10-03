package ownedprocess

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This runs natively on Windows; a cross-build cannot prove Job containment.
func TestWindowsJobTerminatesPipeHoldingDescendant(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	result, err := Run(ctx, fixture("descendant"), Limits{StdoutBytes: 64, StderrBytes: 64})
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("err=%v", err)
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(string(result.Stdout)), 10, 32)
	if err != nil {
		t.Fatalf("missing descendant identity: %q", result.Stdout)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	state, err := windows.WaitForSingleObject(handle, 1000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant retained: state=%d err=%v", state, err)
	}
}
