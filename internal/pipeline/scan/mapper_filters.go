package scan

import (
	"path/filepath"
	"strings"
)

// matchesAny returns true if name starts with any of the given prefixes.
func matchesAny(name string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// isBlockedStdlibCall reports whether pkg.name identifies a standard-library
// call that creates ghost stages — error/log helpers, stdio printf-family,
// sentinel errors. When extracted as stages they poison the diagram with
// non-architectural noise, so they must never appear as a chip, fork branch,
// or fanout target.
func isBlockedStdlibCall(pkg, name string) bool {
	if pkg == "" || name == "" {
		return false
	}
	switch pkg + "." + name {
	case "http.NotFound",
		"log.Warn", "log.Warnf", "log.Error", "log.Errorf",
		"log.Fatal", "log.Fatalf", "log.Print", "log.Printf", "log.Println",
		"fmt.Print", "fmt.Printf", "fmt.Println",
		"fmt.Fprint", "fmt.Fprintf", "fmt.Fprintln",
		"fmt.Sprint", "fmt.Sprintf", "fmt.Errorf",
		"errors.New", "errors.Is", "errors.As", "errors.Unwrap", "errors.Join",
		"slog.Info", "slog.Warn", "slog.Error", "slog.Debug":
		return true
	}
	return false
}

// isUtilitySymbol returns true when the bare symbol name is on the utility
// deny-list. These exported names are utility helpers and must be suppressed
// regardless of which file they land in — e.g. FindProjectRoot declared in a
// phase file (validate.go) would otherwise be emitted as a pipeline stage.
func isUtilitySymbol(name string) bool {
	switch name {
	case "ScanOutputPath", "Resolve", "DataDir", "UserDir",
		"ProjectName", "FindProjectRoot", "AsSummary", "Single":
		return true
	}
	return false
}

// isUtilityFile returns true for files whose basename stem indicates a
// utility role. Symbols from these files provide helpers, not pipeline
// steps, so they should not appear as stages even when exported.
func isUtilityFile(path string) bool {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".go"))
	base = strings.TrimSuffix(base, "_test")
	switch base {
	case "paths", "dirs", "util", "helper", "helpers", "constants":
		return true
	}
	return false
}
