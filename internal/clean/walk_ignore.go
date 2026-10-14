package clean

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadIgnorePatterns reads the clean ignore file (default .pancleanignore)
// from the scan root. Format: one pattern per
// line; # comments and blank lines are skipped. Missing files mean no
// suppression.
func loadIgnorePatterns(root, name string) []string {
	if name == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// markSuppressed flags files matching ignore-file patterns.
func markSuppressed(files []FileInfo, patterns []string) {
	if len(patterns) == 0 {
		return
	}
	for i := range files {
		for _, pattern := range patterns {
			if matchesIgnorePattern(pattern, files[i].RelPath) {
				files[i].Suppressed = true
				break
			}
		}
	}
}

func matchesIgnorePattern(pattern, relPath string) bool {
	dirPattern := strings.HasSuffix(pattern, "/")
	pattern = filepath.Clean(pattern)
	relPath = filepath.Clean(relPath)
	if dirPattern {
		prefix := strings.TrimSuffix(pattern, string(os.PathSeparator))
		return relPath == prefix || strings.HasPrefix(relPath, prefix+string(os.PathSeparator))
	}
	if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
		return true
	}
	matched, err := filepath.Match(pattern, filepath.Base(relPath))
	return err == nil && matched
}
