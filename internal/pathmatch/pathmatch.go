// Package pathmatch provides doublestar-style glob matching over
// slash-separated paths: each pattern segment is a path.Match pattern and
// "**" matches zero or more whole segments.
package pathmatch

import (
	"fmt"
	"path"
	"strings"
)

// Match reports whether value matches pattern. Patterns and values are
// slash-separated paths; a malformed segment pattern never matches.
func Match(pattern, value string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(value, "/"))
}

// MatchAny reports whether value matches at least one pattern.
func MatchAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if Match(pattern, value) {
			return true
		}
	}
	return false
}

// Validate rejects patterns that can never match: empty or blank patterns and
// segments with invalid path.Match syntax.
func Validate(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("empty glob pattern")
	}
	for _, segment := range strings.Split(pattern, "/") {
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
		}
	}
	return nil
}

func matchSegments(pattern, value []string) bool {
	if len(pattern) == 0 {
		return len(value) == 0
	}
	if pattern[0] == "**" {
		return matchSegments(pattern[1:], value) || len(value) > 0 && matchSegments(pattern, value[1:])
	}
	if len(value) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], value[0])
	return err == nil && matched && matchSegments(pattern[1:], value[1:])
}
