package improve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type jinnRequest struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

type jinnResponse struct {
	OK        bool   `json:"ok"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// jinnCall preserves Pan improvement's optional external exploration protocol while
// keeping Pan's reader policy authoritative. The executable receives one JSON
// request on stdin and cannot select a mutating tool.
func (r *providerReader) jinnCall(ctx context.Context, tool string, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := r.validateToolPaths(tool, args); err != nil {
		return "", err
	}
	bin, err := exec.LookPath(r.jinnBin)
	if err != nil {
		return "", fmt.Errorf("resolve jinn binary: %w", err)
	}
	body, err := json.Marshal(jinnRequest{Tool: tool, Args: args})
	if err != nil {
		return "", fmt.Errorf("marshal jinn request: %w", err)
	}
	stdout, stderr := &boundedBuffer{limit: max(r.limit+64<<10, 128<<10)}, &boundedBuffer{limit: 8 << 10}
	runErr := runProviderCommand(ctx, providerCommand{path: bin, dir: r.root, args: []string{r.jinnBin}, stdin: bytes.NewReader(body), stdout: stdout, stderr: stderr})
	// A process can still write a well-formed response while the caller has
	// cancelled its request. Cancellation wins so a timed-out provider run is
	// never reported as a successful exploration result.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var response jinnResponse
	if !stdout.truncated && json.Unmarshal(stdout.Bytes(), &response) == nil {
		if !response.OK {
			return "", fmt.Errorf("jinn %s: %s (code %s)", tool, response.Error, response.ErrorCode)
		}
		return filterJinnResult(tool, response.Result), nil
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("run jinn %s: %w: %s", tool, runErr, strings.TrimSpace(stderr.String()))
	}
	if stdout.truncated {
		return "", fmt.Errorf("jinn %s response exceeds output limit", tool)
	}
	return "", fmt.Errorf("decode jinn %s response", tool)
}

// runProviderCommand keeps external read-only tooling cancellable without
// inheriting terminal pipes. Callers supply bounded writers, so killing the
// child cannot leave Wait blocked on an unconsumed output pipe.
type providerCommand struct {
	path   string
	dir    string
	args   []string
	stdin  io.Reader
	stdout *boundedBuffer
	stderr *boundedBuffer
}

func runProviderCommand(ctx context.Context, input providerCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := &exec.Cmd{Path: input.path, Args: input.args, Dir: input.dir, Stdin: input.stdin, Stdout: input.stdout, Stderr: input.stderr, WaitDelay: 2 * time.Second}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(cmd.WaitDelay):
		}
		return ctx.Err()
	}
}

func filterJinnResult(tool, result string) string {
	if tool != providerSearchFiles && tool != providerFindFiles && tool != providerListDir {
		return result
	}
	kept := make([]string, 0)
	for _, line := range strings.Split(result, "\n") {
		path := strings.TrimPrefix(strings.TrimSpace(line), "./")
		if tool == providerSearchFiles {
			path, _, _ = strings.Cut(path, ":")
		}
		if path == "" || deniedProviderPath(path) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return "[no matches: excluded paths filtered]"
	}
	return strings.Join(kept, "\n")
}

type boundedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.truncated = true
		return written, nil
	}
	_, err := b.Buffer.Write(data)
	return written, err
}

func (r *providerReader) validateToolPaths(tool string, args map[string]any) error {
	if err := r.validateSingleToolPaths(args); err != nil {
		return err
	}
	if err := validateJinnPatterns(tool, args); err != nil {
		return err
	}
	if tool != providerMultiRead {
		return nil
	}
	return r.validateMultiReadPaths(args)
}

func (r *providerReader) validateSingleToolPaths(args map[string]any) error {
	for _, name := range []string{providerPathKey, "dir"} {
		if path := stringArg(args, name); path != "" {
			if _, _, err := r.path(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateJinnPatterns(tool string, args map[string]any) error {
	for _, name := range []string{providerIncludeKey, providerPatternKey} {
		pattern := stringArg(args, name)
		if pattern != "" && (name == providerIncludeKey || tool == providerFindFiles) && unsafeJinnPattern(pattern) {
			return fmt.Errorf("jinn: pattern %q can expose excluded paths", pattern)
		}
	}
	return nil
}

func (r *providerReader) validateMultiReadPaths(args map[string]any) error {
	files, ok := args[providerFilesKey].([]any)
	if !ok || len(files) == 0 || len(files) > 20 {
		return errors.New("multi_read requires 1 to 20 files")
	}
	for _, entry := range files {
		file, ok := entry.(map[string]any)
		if !ok || stringArg(file, providerPathKey) == "" {
			return errors.New("multi_read files must include paths")
		}
		if _, _, err := r.path(stringArg(file, providerPathKey)); err != nil {
			return err
		}
	}
	return nil
}

func unsafeJinnPattern(pattern string) bool {
	if pattern == "" {
		return false
	}
	literal := strings.NewReplacer("*", "", "?", "", "[", "", "]", "").Replace(pattern)
	return deniedProviderPath(filepath.ToSlash(literal))
}
