package scan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const maxGitErrorBytes = 4 << 10

// readGitBounded runs one read-only Git command, retaining at most outputCap
// bytes. Callers own the command arguments, timeout, and output parser; this
// helper owns the shared subprocess, cancellation, and overflow lifecycle.
func readGitBounded(ctx context.Context, root string, outputCap int, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("git stdout: %w", err)
	}
	var stderr limitWriter
	stderr.remaining = maxGitErrorBytes
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start git: %w", err)
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, int64(outputCap)+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", fmt.Errorf("read git output: %w", readErr)
	}
	if len(out) > outputCap {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", fmt.Errorf("git output exceeded %d bytes", outputCap)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("git command: %w: %s", err, detail)
		}
		return "", fmt.Errorf("git command: %w", err)
	}
	return string(out), nil
}

type limitWriter struct {
	bytes.Buffer
	remaining int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	original := len(p)
	if len(p) > w.remaining {
		p = p[:w.remaining]
	}
	_, _ = w.Buffer.Write(p)
	w.remaining -= len(p)
	return original, nil
}
