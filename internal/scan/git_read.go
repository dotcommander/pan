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

// gitReadOnlyCommand reports whether the named git subcommand is a
// read-only operation that readGitBounded may execute; every scan caller
// uses one of these.
func gitReadOnlyCommand(name string) bool {
	switch name {
	case "blame", "describe", "diff", "log", "ls-files", "rev-parse", "show":
		return true
	}
	return false
}

// gitSubcommand skips one leading `git -c key=value` configuration pair so
// the subcommand itself can be checked against the allowlist.
func gitSubcommand(args []string) (string, bool) {
	i := 0
	if len(args) >= 3 && args[0] == "-c" {
		i = 2
	}
	if i >= len(args) {
		return "", false
	}
	return args[i], gitReadOnlyCommand(args[i])
}

// readGitBounded runs one read-only Git command, retaining at most outputCap
// bytes. Callers own the command arguments, timeout, and output parser; this
// helper owns the shared subprocess, cancellation, and overflow lifecycle.
func readGitBounded(ctx context.Context, root string, outputCap int, args ...string) (string, error) {
	if _, ok := gitSubcommand(args); !ok {
		return "", fmt.Errorf("git subcommand not allowlisted for read-only use: %v", args)
	}
	// The command name is a constant and every argument passes the
	// read-only allowlist: git runs as one direct argv process in root,
	// never through a shell.
	cmd := exec.CommandContext(ctx, "git")
	cmd.Dir = root
	cmd.Args = append(cmd.Args, args...)
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
