package improve

import "strings"

// DiffLines measures a whole-file replacement as a line-multiset diff.
func DiffLines(oldContents, newContents string) (added, deleted int) {
	counts := make(map[string]int)
	for _, line := range splitLines(oldContents) {
		counts[line]++
	}
	for _, line := range splitLines(newContents) {
		counts[line]--
	}
	for _, delta := range counts {
		if delta > 0 {
			deleted += delta
		} else {
			added += -delta
		}
	}
	return added, deleted
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
