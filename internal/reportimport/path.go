package reportimport

import (
	"fmt"
	"strings"
	"unicode"
)

func validPath(path string, canonical bool, l Limits) error {
	if path == "" || len(path) > l.PathBytes {
		return fmt.Errorf("missing or excessive path")
	}
	for _, c := range path {
		if unicode.IsControl(c) {
			return fmt.Errorf("control character in path")
		}
	}
	portable := strings.ReplaceAll(path, `\`, "/")
	for _, part := range strings.Split(portable, "/") {
		if part == ".." {
			return fmt.Errorf("traversal path is unsupported: %q", path)
		}
		if canonical && (part == "" || part == ".") {
			return fmt.Errorf("SCIP path must be canonical relative slash path: %q", path)
		}
	}
	drive := len(path) >= 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':'
	if drive && (len(path) < 3 || (path[2] != '/' && path[2] != '\\')) {
		return fmt.Errorf("drive-relative path is ambiguous: %q", path)
	}
	if canonical && (strings.ContainsAny(path, `\:`) || strings.HasPrefix(path, "/")) {
		return fmt.Errorf("SCIP path must be canonical relative slash path: %q", path)
	}
	return nil
}
