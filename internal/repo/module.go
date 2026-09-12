package repo

import (
	"os"
	"path/filepath"
	"strings"
)

// ModulePath returns the Go module path declared by root's go.mod, or "" when
// root is not a Go module or the file cannot be read. The module path lets the
// ranking layer resolve import edges to repository directories; without it,
// ranking falls back to suffix matching and labels the resolution accordingly.
func ModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(after), `"`)
		}
	}
	return ""
}
