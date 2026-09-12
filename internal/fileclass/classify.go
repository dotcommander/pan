// Package fileclass derives deterministic repository-file roles without
// changing the persisted analysis snapshot schema.
package fileclass

import (
	"path/filepath"
	"slices"
	"strings"
)

var validClasses = []string{string(Generated), string(Vendor), string(Test), string(Fixture), string(Docs), string(Example), string(Data), string(Production), string(Unknown), "all"}

// Valid reports whether value is an accepted CLI class selector.
func Valid(value string) bool { return slices.Contains(validClasses, value) }

// Class describes the role a file plays in a repository.
type Class string

const (
	Generated  Class = "generated"
	Vendor     Class = "vendor"
	Test       Class = "test"
	Fixture    Class = "fixture"
	Docs       Class = "docs"
	Example    Class = "example"
	Data       Class = "receipt/data"
	Production Class = "production"
	Unknown    Class = "unknown"
)

// Classify returns a precedence-ordered role derived from repository-relative
// identity facts. Paths are normalized to slash form before matching.
func Classify(filePath, language string, generated bool) Class {
	if generated {
		return Generated
	}
	portable := strings.ReplaceAll(filePath, `\`, "/")
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(portable)), "./")
	lower := strings.ToLower(clean)
	segments := strings.Split(lower, "/")
	if hasSegment(segments, "generated") {
		return Generated
	}
	if hasSegment(segments, "vendor") || hasSegment(segments, "node_modules") || hasSegment(segments, "third_party") {
		return Vendor
	}
	base := segments[len(segments)-1]
	if strings.HasSuffix(base, "_test.go") || hasSegment(segments, "test") || hasSegment(segments, "tests") {
		return Test
	}
	if hasSegment(segments, "testdata") || hasSegment(segments, "fixtures") || hasSegment(segments, "fixture") {
		return Fixture
	}
	if hasSegment(segments, "docs") || base == "readme.md" || base == "contributing.md" || base == "changelog.md" || base == "license.md" {
		return Docs
	}
	if hasSegment(segments, "example") || hasSegment(segments, "examples") || strings.HasPrefix(base, "example_") {
		return Example
	}
	if hasSegment(segments, ".work") || hasSegment(segments, "receipts") || hasSegment(segments, "artifacts") || hasSegment(segments, "benchmarks") || strings.HasSuffix(base, ".jsonl") {
		return Data
	}
	if language != "" && language != "unknown" && language != "markdown" {
		return Production
	}
	return Unknown
}

func hasSegment(segments []string, want string) bool {
	for _, segment := range segments {
		if segment == want {
			return true
		}
	}
	return false
}
