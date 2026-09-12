package clean

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/config"
)

// CheckCompleteness analyzes walked paths for missing healthy-repository
// files. Every expectation, CI pattern, and build pattern comes from the
// configured CleanRules; this function only applies them.
func CheckCompleteness(files []FileInfo, rules config.CleanRules) CompletenessReport {
	pathSet := make(map[string]bool, len(files))
	for _, f := range files {
		pathSet[filepath.ToSlash(f.RelPath)] = true
	}

	var missing []MissingItem
	for _, expected := range rules.CompletenessExpected {
		found := false
		for _, name := range expected.Names {
			if pathSet[name] {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, MissingItem{
				Name:     expected.Names[0],
				Severity: expected.Severity,
				Why:      expected.Why,
			})
		}
	}

	if !matchesAnyPattern(files, rules.CIPatterns) {
		missing = append(missing, MissingItem{
			Name:     "CI config",
			Severity: sevLabelWarning,
			Why:      "no CI/CD configuration — code is not automatically tested or deployed",
		})
	}
	if !matchesAnyPattern(files, rules.BuildPatterns) {
		missing = append(missing, MissingItem{
			Name:     "Build system",
			Severity: sevLabelInfo,
			Why:      "no Makefile, Taskfile, or build config — build process is undocumented",
		})
	}

	score := 100
	for _, m := range missing {
		switch m.Severity {
		case sevLabelError:
			score -= 20
		case sevLabelWarning:
			score -= 10
		case sevLabelInfo:
			score -= 5
		}
	}
	if score < 0 {
		score = 0
	}
	return CompletenessReport{Missing: missing, Score: score}
}

// matchesAnyPattern reports whether any walked path matches one of the glob
// patterns. Patterns without a slash also match by base name.
func matchesAnyPattern(files []FileInfo, patterns []string) bool {
	for _, f := range files {
		rel := filepath.ToSlash(f.RelPath)
		base := path.Base(rel)
		for _, pattern := range patterns {
			if matched, err := path.Match(pattern, rel); err == nil && matched {
				return true
			}
			if !strings.Contains(pattern, "/") && base == pattern {
				return true
			}
		}
	}
	return false
}
