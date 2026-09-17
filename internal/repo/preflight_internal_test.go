package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreflightCollapseWhitespaceMatchesLegacyASCIIClass(t *testing.T) {
	t.Parallel()
	const value = "alpha\t \r\n\fbeta\u00a0gamma"
	const want = "alpha beta\u00a0gamma"
	if got := preflightCollapseWhitespace(value); got != want {
		t.Fatalf("preflightCollapseWhitespace(%q) = %q, want %q", value, got, want)
	}
}

func TestPreflightRejectsMissingRoot(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := Preflight(filepath.Join(missing, "file.txt"), missing, false)
	if err == nil || !strings.Contains(err.Error(), "repository root must be a directory") {
		t.Fatalf("err = %v, want missing-root directory error", err)
	}
}

func TestPreflightSummaryTruncatesOnRuneBoundary(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "README.md")
	line := strings.Repeat("é", 400)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	summary := preflightSummary(path)
	if !utf8.ValidString(summary) {
		t.Fatalf("summary is not valid UTF-8: %q", summary)
	}
	if got := len([]rune(summary)); got != 280 {
		t.Fatalf("summary rune count = %d, want 280", got)
	}
}
