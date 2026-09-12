package scan

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxAuditHistoryOutput = 4 * 1024 * 1024

type AuditHistoryReport struct {
	SchemaVersion int                    `json:"schema_version"`
	Root          string                 `json:"root"`
	Hotspots      []AuditHistoryHotspot  `json:"hotspots"`
	Couplings     []AuditHistoryCoupling `json:"couplings"`
	Omissions     []AuditHistoryOmission `json:"omissions,omitempty"`
}

type AuditHistoryHotspot struct {
	ID            string                `json:"id"`
	Path          string                `json:"path"`
	RelativeChurn int                   `json:"relative_churn"`
	Touches       int                   `json:"touches"`
	LastTouched   string                `json:"last_touched"`
	CoChanges     []AuditHistoryPartner `json:"co_changes,omitempty"`
}

type AuditHistoryCoupling struct {
	ID         string   `json:"id"`
	Paths      []string `json:"paths"`
	Commits    int      `json:"commits"`
	Confidence float64  `json:"confidence_score"`
}

type AuditHistoryPartner struct {
	Path       string  `json:"path"`
	Commits    int     `json:"commits"`
	Confidence float64 `json:"confidence"`
}

type AuditHistoryOmission struct {
	Commit string `json:"commit"`
	Reason string `json:"reason"`
}

type auditHistoryFile struct {
	touches int
	churn   int
	last    time.Time
	with    map[string]int
}

type auditHistoryCommit struct {
	hash, subject string
	when          time.Time
	stats         map[string]int
}

// AuditHistory collects Pan-compatible, commit-count-bounded numstat
// history for currently tracked non-test Go files.
func AuditHistory(ctx context.Context, root string, window int) (AuditHistoryReport, error) {
	empty := AuditHistoryReport{SchemaVersion: 1, Root: root, Hotspots: []AuditHistoryHotspot{}, Couplings: []AuditHistoryCoupling{}}
	if window <= 0 {
		return empty, nil
	}
	trackedOut, err := auditGit(ctx, root, "ls-files", "-z", "--", "*.go")
	if err != nil {
		return AuditHistoryReport{}, fmt.Errorf("list tracked Go files: %w", err)
	}
	tracked := map[string]struct{}{}
	for _, path := range splitNUL(trackedOut) {
		path = filepath.ToSlash(path)
		if path != "" && !isTestPath(path) {
			tracked[path] = struct{}{}
		}
	}
	out, err := auditGit(ctx, root, "log", "--no-merges", "-n", strconv.Itoa(window), "--numstat", "--format=%x1e%H%x1f%ct%x1f%s")
	if err != nil {
		return AuditHistoryReport{}, fmt.Errorf("collect git history: %w", err)
	}
	files := map[string]*auditHistoryFile{}
	for _, commit := range parseAuditHistory(out) {
		if auditDependencyCommit(commit.subject) {
			empty.Omissions = append(empty.Omissions, AuditHistoryOmission{Commit: commit.hash, Reason: "dependency-only commit"})
			continue
		}
		paths := make([]string, 0, len(commit.stats))
		for path := range commit.stats {
			if _, ok := tracked[path]; ok {
				paths = append(paths, path)
			}
		}
		if len(paths) > 20 {
			empty.Omissions = append(empty.Omissions, AuditHistoryOmission{Commit: commit.hash, Reason: "touches more than 20 scoped files"})
			continue
		}
		slices.Sort(paths)
		for _, path := range paths {
			entry := files[path]
			if entry == nil {
				entry = &auditHistoryFile{with: map[string]int{}}
				files[path] = entry
			}
			entry.touches++
			entry.churn += commit.stats[path]
			if commit.when.After(entry.last) {
				entry.last = commit.when
			}
			for _, partner := range paths {
				if partner != path {
					entry.with[partner]++
				}
			}
		}
	}
	for path, entry := range files {
		h := AuditHistoryHotspot{ID: "pan:history:hotspot:" + historySlug(path), Path: path, RelativeChurn: entry.churn, Touches: entry.touches}
		if !entry.last.IsZero() {
			h.LastTouched = entry.last.UTC().Format(time.RFC3339)
		}
		for partner, commits := range entry.with {
			confidence := float64(commits) / float64(entry.touches)
			if commits >= 3 && confidence >= .5 {
				h.CoChanges = append(h.CoChanges, AuditHistoryPartner{Path: partner, Commits: commits, Confidence: confidence})
			}
		}
		slices.SortFunc(h.CoChanges, func(a, b AuditHistoryPartner) int {
			if a.Commits != b.Commits {
				return b.Commits - a.Commits
			}
			return strings.Compare(a.Path, b.Path)
		})
		if len(h.CoChanges) > 4 {
			h.CoChanges = h.CoChanges[:4]
		}
		empty.Hotspots = append(empty.Hotspots, h)
		for _, partner := range h.CoChanges {
			if path < partner.Path {
				empty.Couplings = append(empty.Couplings, AuditHistoryCoupling{ID: "pan:history:coupling:" + historySlug(path+"-"+partner.Path), Paths: []string{path, partner.Path}, Commits: partner.Commits, Confidence: partner.Confidence})
			}
		}
	}
	slices.SortFunc(empty.Hotspots, func(a, b AuditHistoryHotspot) int {
		if a.RelativeChurn != b.RelativeChurn {
			return b.RelativeChurn - a.RelativeChurn
		}
		if a.Touches != b.Touches {
			return b.Touches - a.Touches
		}
		if a.LastTouched != b.LastTouched {
			return strings.Compare(b.LastTouched, a.LastTouched)
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(empty.Hotspots) > 12 {
		empty.Hotspots = empty.Hotspots[:12]
	}
	slices.SortFunc(empty.Couplings, func(a, b AuditHistoryCoupling) int {
		if a.Commits != b.Commits {
			return b.Commits - a.Commits
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(empty.Couplings) > 12 {
		empty.Couplings = empty.Couplings[:12]
	}
	return empty, nil
}

func auditGit(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return readGitBounded(ctx, root, maxAuditHistoryOutput, args...)
}

func parseAuditHistory(out string) []auditHistoryCommit {
	var commits []auditHistoryCommit
	for _, block := range strings.Split(out, "\x1e") {
		block = strings.TrimPrefix(block, "\n")
		if block == "" {
			continue
		}
		parts := strings.SplitN(block, "\n", 2)
		header := strings.Split(parts[0], "\x1f")
		if len(header) != 3 {
			continue
		}
		unix, _ := strconv.ParseInt(header[1], 10, 64)
		c := auditHistoryCommit{hash: header[0], subject: header[2], when: time.Unix(unix, 0).UTC(), stats: map[string]int{}}
		if len(parts) == 2 {
			for _, line := range strings.Split(parts[1], "\n") {
				fields := strings.SplitN(line, "\t", 3)
				if len(fields) != 3 {
					continue
				}
				add, e1 := strconv.Atoi(fields[0])
				del, e2 := strconv.Atoi(fields[1])
				if e1 == nil && e2 == nil {
					c.stats[filepath.ToSlash(fields[2])] += add + del
				}
			}
		}
		commits = append(commits, c)
	}
	return commits
}

func auditDependencyCommit(subject string) bool {
	s := strings.ToLower(subject)
	return strings.Contains(s, "dependabot") || strings.Contains(s, "renovate") || strings.HasPrefix(s, "deps:") || strings.HasPrefix(s, "chore(deps)")
}
func historySlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
