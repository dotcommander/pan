package gitoutgoing

import (
	"context"
	"errors"
	"fmt"
	"github.com/dotcommander/pan/internal/config"
	"os"
	"path/filepath"
	"strings"
)

// Check scans the outgoing revision and returns the legacy findings. It never mutates Git state.
func Check(revision, repositoryRoot string, maxBlobBytes int64) ([]Finding, error) {
	report, err := Inspect(revision, repositoryRoot, maxBlobBytes)
	return report.Findings, err
}

func CheckContext(ctx context.Context, revision, repositoryRoot string, maxBlobBytes int64, rules config.OutgoingGitRules) ([]Finding, error) {
	report, err := InspectContext(ctx, revision, repositoryRoot, maxBlobBytes, rules)
	return report.Findings, err
}

// Inspect returns typed evidence for every outgoing path and generic safety finding.
func Inspect(revision, repositoryRoot string, maxBlobBytes int64) (Report, error) {
	return InspectContext(context.Background(), revision, repositoryRoot, maxBlobBytes, config.OutgoingGitRules{})
}

func InspectContext(ctx context.Context, revision, repositoryRoot string, maxBlobBytes int64, rules config.OutgoingGitRules) (Report, error) {
	rules = rules.Normalized()
	if rules.Timeout < 0 || rules.MaxLogBytes < 0 || rules.MaxHeaderBytes < 0 || rules.MaxStderrBytes < 0 {
		return Report{}, errors.New("negative outgoing Git limit")
	}
	ctx, cancel := context.WithTimeout(ctx, rules.Timeout)
	defer cancel()
	if maxBlobBytes < 0 {
		return Report{}, errors.New("maximum blob size must not be negative")
	}
	root, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return Report{}, fmt.Errorf("cannot resolve repository root %q: %w", repositoryRoot, err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Report{}, fmt.Errorf("repository root is not a directory: %q", root)
	}
	// Transport cancellation may surface as a closed-pipe error; retain both
	// that diagnostic and the operation cancellation identity at the public boundary.
	report, err := scanContext(ctx, root, revision, maxBlobBytes, rules)
	return report, errors.Join(err, ctx.Err())
}

// Emit writes the legacy diagnostic format and reports whether the result blocks pushing.
func Emit(findings []Finding, maxBlobBytes int64, stderr interface{ Write([]byte) (int, error) }) bool {
	blocked := false
	for _, f := range findings {
		if f.Rule == "large-blob" {
			fmt.Fprintf(stderr, "pre-push: BLOCKED: outgoing blob exceeds %d bytes: rule=%s commit=%s path=%s size=%d\n", maxBlobBytes, f.Rule, f.Commit, quoteGitPath(f.Path), f.Size)
			blocked = true
			continue
		}
		fmt.Fprintf(stderr, "pre-push: %s: rule=%s commit=%s path=%s\n", strings.ToUpper(f.Level), f.Rule, f.Commit, quoteGitPath(f.Path))
		blocked = blocked || f.Level == "blocked"
	}
	return blocked
}
