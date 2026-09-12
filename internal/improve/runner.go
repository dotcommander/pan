package improve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxTestOutputBytes bounds the raw `go test` output retained in
// TestResult.Output for forensics. Event counting always sees every line
// regardless of this cap.
const maxTestOutputBytes = 1 << 20

// defaultTestTimeout bounds one suite run when no configured timeout
// applies. It is a safety bound, not configuration: policy timeouts live
// in the improve configuration.
const defaultTestTimeout = 5 * time.Minute

// cmdGo is the constant go binary name; every argument arrives through
// cmd.Args so the toolchain always runs as one direct argv process, never
// through a shell.
const cmdGo = "go"

const goTestSubcommand = "test"

// Toolchain runs the local Go toolchain against a repository directory.
// It never invokes a shell: every invocation is a direct-argv command.
// Test and vet runs execute the target's code, so guarded workflows point
// it at an isolated copy, never at the real target.
type Toolchain struct {
	// TestTimeout bounds one test-suite run; zero selects the built-in
	// default bound.
	TestTimeout time.Duration
}

// LookGo verifies the go binary is available before any gated work starts.
func LookGo() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain not found on PATH: %w", err)
	}
	return nil
}

// LookStaticcheck verifies the optional Pan improvement-compatible static analysis
// executable before a guarded run reaches proposal generation.
func LookStaticcheck() error {
	if _, err := exec.LookPath("staticcheck"); err != nil {
		return fmt.Errorf("staticcheck not found on PATH (install: go install honnef.co/go/tools/cmd/staticcheck@latest): %w", err)
	}
	return nil
}

// RunTests executes the uncached test suite with coverage and folds the
// profile into result. A failing test reports AllPassed=false, not an
// error; only context cancellation and process failures return errors.
func (t Toolchain) RunTests(ctx context.Context, dir string) (TestResult, error) {
	timeout := t.TestTimeout
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}
	profile, err := os.CreateTemp("", "pan-improve-cover-*.out")
	if err != nil {
		return TestResult{}, fmt.Errorf("create coverprofile: %w", err)
	}
	profilePath := profile.Name()
	if closeErr := profile.Close(); closeErr != nil {
		_ = os.Remove(profilePath)
		return TestResult{}, fmt.Errorf("close coverprofile: %w", closeErr)
	}
	defer func() { _ = os.Remove(profilePath) }()

	args := []string{
		goTestSubcommand, "-json", "-count=1",
		"-timeout", timeout.String(),
		"-coverprofile=" + profilePath, "-covermode=set",
		"./...",
	}
	cmd := exec.CommandContext(ctx, cmdGo)
	cmd.Dir = dir
	cmd.Args = append(cmd.Args, args...)
	// Stream stdout and stderr through one pipe so unbounded test output is
	// never buffered whole in memory; the parser caps what it retains.
	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdout = pipeWriter
	cmd.Stderr = pipeWriter
	if startErr := cmd.Start(); startErr != nil {
		_ = pipeReader.Close()
		_ = pipeWriter.Close()
		return TestResult{}, fmt.Errorf("start go test: %w", startErr)
	}
	waitErr := make(chan error, 1)
	go func() {
		// Producer: run to completion, then close the writer so the reader
		// observes EOF and terminates.
		waitErr <- cmd.Wait()
		_ = pipeWriter.Close()
	}()
	result, events := parseTestStream(pipeReader, maxTestOutputBytes)
	runErr := <-waitErr
	if ctx.Err() != nil {
		return TestResult{}, ctx.Err()
	}
	result.AllPassed = result.AllPassed && runErr == nil
	if events == 0 && runErr != nil {
		result.Failures = append(result.Failures, strings.TrimSpace(result.Output))
	}

	blocks, _, err := ParseCoverProfile(profilePath, ReadModulePath(dir))
	if err != nil {
		return TestResult{}, fmt.Errorf("parse suite coverprofile: %w", err)
	}
	result.ProfileMeasured = true
	result.Files = FileCoverages(blocks)
	for _, block := range blocks {
		result.TotalStmts += block.Statements
		if block.Count > 0 {
			result.CoveredStmts += block.Statements
		}
	}
	if result.TotalStmts > 0 {
		result.TotalCoverage = math.Round(100*float64(result.CoveredStmts)/float64(result.TotalStmts)*100) / 100
	}
	return result, nil
}

// Vet runs `go vet` on the changed package directories only, mirroring
// Pan improvement: vetting every package would fail a valid proposal over a
// pre-existing vet failure in an untouched package.
func (t Toolchain) Vet(ctx context.Context, dir string, packages []string) error {
	if len(packages) == 0 {
		return nil
	}
	args := append([]string{"vet"}, packages...)
	cmd := exec.CommandContext(ctx, cmdGo)
	cmd.Dir = dir
	cmd.Args = append(cmd.Args, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("go vet: %w\n%s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// Staticcheck runs the optional external checker on changed packages. Pan improvement
// ignores diagnostics outside the proposal's changed files, so existing
// unrelated findings do not reject a guarded refactor.
func (t Toolchain) Staticcheck(ctx context.Context, dir string, packages, files []string) error {
	if len(packages) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, "staticcheck", packages...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil || !staticcheckTouchesChangedFile(string(output), files) {
		return nil
	}
	return fmt.Errorf("staticcheck: %w\n%s", err, strings.TrimSpace(string(output)))
}

func staticcheckTouchesChangedFile(output string, files []string) bool {
	for _, file := range files {
		if strings.Contains(output, filepath.ToSlash(file)+":") {
			return true
		}
	}
	return false
}

// testEvent is the subset of `go test -json` event fields pan consumes.
type testEvent struct {
	Action  string
	Package string
	Test    string
}

// parseTestStream decodes `go test -json` events as they arrive, counting
// tests without buffering the whole output. It retains at most outputCap
// bytes of raw output; counting always sees every line regardless of the
// cap. AllPassed starts true and is set false on any fail event; the
// caller ANDs in the process exit status.
func parseTestStream(r io.Reader, outputCap int) (TestResult, int) {
	result := TestResult{AllPassed: true}
	seen := &testSeen{tests: map[string]struct{}{}, skipped: map[string]struct{}{}}
	parsedEvents := 0

	var output strings.Builder
	reader := bufio.NewReader(r)
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			writeCappedLine(&output, line, outputCap)
			parsedEvents += applyTestOutputLine(line, &result, seen)
		}
		if readErr != nil {
			break
		}
	}

	result.Output = output.String()
	result.Tests = sortedKeys(seen.tests)
	result.Skipped = sortedKeys(seen.skipped)
	return result, parsedEvents
}

// testSeen accumulates the test identities observed in one stream.
type testSeen struct {
	tests   map[string]struct{}
	skipped map[string]struct{}
}

// writeCappedLine appends one raw output line while the retained bytes
// stay under cap.
func writeCappedLine(output *strings.Builder, line string, outputCap int) {
	if remaining := outputCap - output.Len(); remaining > 0 {
		if len(line) > remaining {
			output.WriteString(line[:remaining])
		} else {
			output.WriteString(line)
		}
	}
}

// applyTestOutputLine folds one output line into the result when it
// decodes as a test event, returning 1 for a parsed event and 0 otherwise.
func applyTestOutputLine(line string, result *TestResult, seen *testSeen) int {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return 0
	}
	var event testEvent
	if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
		return 0
	}
	applyTestEvent(event, result, seen)
	return 1
}

// applyTestEvent counts one decoded `go test -json` event.
func applyTestEvent(event testEvent, result *TestResult, seen *testSeen) {
	identity := event.Package + "\t" + event.Test
	switch event.Action {
	case "run":
		if event.Test != "" {
			result.TotalTests++
			seen.tests[identity] = struct{}{}
		}
	case "pass":
		if event.Test != "" {
			seen.tests[identity] = struct{}{}
		}
	case "skip":
		if event.Test != "" {
			seen.tests[identity] = struct{}{}
			seen.skipped[identity] = struct{}{}
		}
	case "fail":
		result.AllPassed = false
		if event.Test != "" {
			result.Failures = append(result.Failures, identity)
			seen.tests[identity] = struct{}{}
		}
	}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// ChangedPackages maps proposal file paths to the deduplicated, sorted
// `go vet` package patterns that cover them.
func ChangedPackages(files []string) []string {
	seen := make(map[string]struct{}, len(files))
	var dirs []string
	for _, file := range files {
		dir := filepath.ToSlash(filepath.Dir(filepath.Clean(file)))
		if dir == "." || dir == "" {
			dirs = append(dirs, ".")
			continue
		}
		dir = "./" + dir
		if _, dup := seen[dir]; !dup {
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}
