package scan

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// Bounds on git plumbing output and reported hygiene lists.
const (
	maxGitOutputBytes = 4 << 20
	maxHygieneListed  = 50
)

// HygieneCounts records the path counts behind a HygieneReport.
type HygieneCounts struct {
	Tracked       int `json:"tracked"`
	TrackedSource int `json:"tracked_source"`
	Untracked     int `json:"untracked"`
	UntrackedCode int `json:"untracked_code"`
	Ignored       int `json:"ignored"`
	IgnoredSource int `json:"ignored_source"`
}

// HygieneIssue is one deterministic hygiene lead.
type HygieneIssue struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Evidence string `json:"evidence"`
}

// HygieneReport is the deterministic git hygiene inventory: tracked,
// untracked, and ignored code, plus release-blocking drift issues. When git
// is unavailable or the root is not a work tree, the report degrades to
// snapshot-derived counts with an explicit note instead of failing.
type HygieneReport struct {
	GitAvailable  bool           `json:"git_available"`
	Counts        HygieneCounts  `json:"counts"`
	UntrackedCode []string       `json:"untracked_code,omitempty"`
	IgnoredSource []string       `json:"ignored_source,omitempty"`
	Issues        []HygieneIssue `json:"issues,omitempty"`
	Note          string         `json:"note,omitempty"`
}

// Hygiene inspects tracked, untracked, and ignored paths with git when
// available so ignored source files stay visible to release audits.
func Hygiene(ctx context.Context, root string, snap analyze.Snapshot) HygieneReport {
	report := HygieneReport{GitAvailable: true}
	tracked, err := gitTrackedPaths(ctx, root)
	if err != nil {
		return hygieneFallback(snap, err)
	}
	untracked, _ := gitUntrackedPaths(ctx, root)
	ignored, _ := gitIgnoredPaths(ctx, root)

	report.Counts = HygieneCounts{
		Tracked:       len(tracked),
		TrackedSource: countSourcePaths(tracked),
		Untracked:     len(untracked),
		UntrackedCode: countSourcePaths(untracked),
		Ignored:       len(ignored),
		IgnoredSource: countSourcePaths(ignored),
	}
	report.UntrackedCode = capList(sourcePaths(untracked))
	report.IgnoredSource = capList(sourcePaths(ignored))

	for _, path := range sourcePaths(ignored) {
		if len(report.Issues) >= maxHygieneListed {
			break
		}
		report.Issues = append(report.Issues, HygieneIssue{
			ID: "ignored_source_file", Severity: "high", Path: path,
			Evidence: "source file is ignored by git and will be absent from a tracked-only checkout",
		})
	}
	for _, path := range sourcePaths(untracked) {
		if len(report.Issues) >= maxHygieneListed {
			break
		}
		report.Issues = append(report.Issues, HygieneIssue{
			ID: "untracked_source_file", Severity: confidenceMedium, Path: path,
			Evidence: "source file is untracked; verify whether audit or build behavior depends on local-only code",
		})
	}
	return report
}

// hygieneFallback reports snapshot-derived counts when git evidence is not
// obtainable, keeping the command operational without a provider.
func hygieneFallback(snap analyze.Snapshot, cause error) HygieneReport {
	discovered := len(snap.Files)
	report := HygieneReport{
		GitAvailable: false,
		Counts:       HygieneCounts{TrackedSource: countSourceFiles(snap.Files)},
		Note:         fmt.Sprintf("git unavailable (%s); worktree drift not assessed; counts derived from snapshot of %d discovered files", causeErrorText(cause), discovered),
	}
	return report
}

func causeErrorText(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) > maxEvidence {
		return text[:maxEvidence]
	}
	return text
}

// gitTrackedPaths lists the tracked paths in root.
func gitTrackedPaths(ctx context.Context, root string) ([]string, error) {
	return gitOutputLines(ctx, root, "ls-files", "--cached", "-z")
}

// gitUntrackedPaths lists the untracked, non-ignored paths in root.
func gitUntrackedPaths(ctx context.Context, root string) ([]string, error) {
	return gitOutputLines(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
}

// gitIgnoredPaths lists the untracked paths git ignores in root.
func gitIgnoredPaths(ctx context.Context, root string) ([]string, error) {
	return gitOutputLines(ctx, root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
}

// IgnoredPaths returns Git's authoritative ignored-path inventory so callers
// can keep workspace artifacts outside bounded source-analysis budgets.
func IgnoredPaths(ctx context.Context, root string) ([]string, error) {
	return gitIgnoredPaths(ctx, root)
}

// gitOutputLines runs one read-only Git command and splits its NUL-terminated
// output. readGitBounded owns the shared subprocess and byte bound.
func gitOutputLines(ctx context.Context, root string, args ...string) ([]string, error) {
	out, err := readGitBounded(ctx, root, maxGitOutputBytes, args...)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

func splitNUL(out string) []string {
	if out == "" {
		return nil
	}
	paths := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	return paths
}

func isSourcePath(path string) bool {
	return strings.HasSuffix(path, ".go")
}

func sourcePaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if isSourcePath(p) {
			out = append(out, filepath.ToSlash(p))
		}
	}
	slices.Sort(out)
	return out
}

func countSourcePaths(paths []string) int {
	return len(sourcePaths(paths))
}

func countSourceFiles(files []analyze.File) int {
	total := 0
	for _, file := range files {
		if isSourcePath(file.Path) {
			total++
		}
	}
	return total
}

// capList bounds reported hygiene lists while preserving a sentinel that
// keeps the true total visible.
func capList(paths []string) []string {
	if len(paths) <= maxHygieneListed {
		return paths
	}
	kept := slices.Clone(paths[:maxHygieneListed])
	return append(kept, fmt.Sprintf("... (%d more)", len(paths)-maxHygieneListed))
}
