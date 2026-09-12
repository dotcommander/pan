// Package scan derives deterministic repository evidence packets from an
// analyze.Snapshot plus bounded source reads. Every report is a pure
// function of its inputs: two runs over the same repository state produce
// deeply equal reports. Git-backed packets (Hygiene, Changes) degrade to
// explicit notes when git or history is unavailable, so pan keeps operating
// without any provider, model, or network access.
package scan

import (
	"bufio"
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"unicode"
)

// Bounds on inspected source keep every packet bounded regardless of
// repository shape. Per-file and per-report caps mirror the truncation
// accounting style of the Pan audit packets pan is grounded in.
const (
	maxLineBytes  = 1 << 20 // longest single source line inspected
	maxScanLines  = 20000   // lines inspected per file
	maxEvidence   = 160     // characters kept in an evidence excerpt
	perFileHitCap = 12      // hits/effects kept per file
	maxReasons    = 12      // risk reasons kept per file
)

// sourceLine is one inspected line of a source file.
type sourceLine struct {
	number int
	text   string
}

// readLines reads bounded lines from path. It reports truncated=true when
// the line or line-count bound stopped inspection early, so callers can
// record the truncation instead of silently reporting partial evidence.
func readLines(ctx context.Context, path string) ([]sourceLine, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	var lines []sourceLine
	truncated := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if len(lines) >= maxScanLines {
			truncated = true
			break
		}
		lines = append(lines, sourceLine{number: len(lines) + 1, text: strings.TrimSpace(scanner.Text())})
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return lines, true, nil
		}
		return nil, false, err
	}
	return lines, truncated, nil
}

// evidence normalizes a source line into a bounded excerpt.
func evidence(text string) string {
	joined := strings.Join(strings.Fields(text), " ")
	if len(joined) <= maxEvidence {
		return joined
	}
	return joined[:maxEvidence-3] + "..."
}

// isTestPath reports whether path is test source or test fixture data.
func isTestPath(path string) bool {
	return strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/testdata/")
}

// kebabName converts a CamelCase struct field name into its kebab-case
// command or flag identifier, preserving acronym runs (APIKey → api-key).
func kebabName(value string) string {
	runes := []rune(value)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) &&
			(unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
				(i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// pathTermMatches reports whether a lowercase slash path contains term.
// Terms containing punctuation match by substring; bare words match on
// word boundaries so "auth" does not match "author".
func pathTermMatches(path, term string) bool {
	if strings.ContainsAny(term, "./-") {
		return strings.Contains(path, term)
	}
	re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(term) + `([^a-z0-9]|$)`)
	return re.MatchString(path)
}
