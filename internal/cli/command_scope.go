package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func validateExclusionPaths(paths []string) error {
	for _, value := range paths {
		cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
		if filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return fmt.Errorf("--exclude-path must name a path inside the repository: %q", value)
		}
	}
	return nil
}

// commandContext keeps streaming commands alive until their caller cancels
// them. Other commands retain the configured execution deadline.
func commandContext(parent context.Context, path []string, timeout time.Duration) (context.Context, context.CancelFunc) {
	if isLongLivedCommand(path) {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func isLongLivedCommand(path []string) bool {
	return slices.Equal(path, []string{flowCommand, serveCmd}) ||
		slices.Equal(path, []string{"agent", serveCmd}) ||
		slices.Equal(path, []string{"agent", "stdio"})
}
