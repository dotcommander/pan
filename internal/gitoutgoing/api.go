package gitoutgoing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Check scans the outgoing revision and returns the legacy findings. It never mutates Git state.
func Check(revision, repositoryRoot string, maxBlobBytes int64) ([]Finding, error) {
	report, err := Inspect(revision, repositoryRoot, maxBlobBytes)
	return report.Findings, err
}

// Inspect returns typed evidence for every outgoing path and generic safety finding.
func Inspect(revision, repositoryRoot string, maxBlobBytes int64) (Report, error) {
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
	return scan(root, revision, maxBlobBytes)
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
