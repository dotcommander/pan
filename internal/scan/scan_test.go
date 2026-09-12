package scan

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

// writeTree writes rel -> content files under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// goSnapshot builds a minimal snapshot of Go files rooted at root.
func goSnapshot(root string, rels ...string) analyze.Snapshot {
	snap := analyzeFiles(makeGoFiles(rels...)...)
	snap.Root = root
	return snap
}

func makeGoFiles(rels ...string) []analyze.File {
	files := make([]analyze.File, 0, len(rels))
	for _, rel := range rels {
		files = append(files, analyze.File{Path: rel, Language: languageGo})
	}
	return files
}

func analyzeFiles(files ...analyze.File) analyze.Snapshot {
	return analyze.Snapshot{Files: files}
}

func TestEvidenceBoundsAndCollapses(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", maxEvidence+10)
	if got := evidence(long); len(got) != maxEvidence || !strings.HasSuffix(got, "...") {
		t.Fatalf("evidence(long) = %q (len %d), want %d chars ending in ...", got, len(got), maxEvidence)
	}
	if got := evidence("  hello   world  "); got != "hello world" {
		t.Fatalf("evidence collapsed = %q, want %q", got, "hello world")
	}
}

func TestKebabName(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{in: "Repo", want: "repo"},
		{in: "APIKey", want: "api-key"},
		{in: "HTTPServerURL", want: "http-server-url"},
		{in: "top", want: "top"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := kebabName(tt.in); got != tt.want {
				t.Fatalf("kebabName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPathTermMatchesWordBoundary(t *testing.T) {
	t.Parallel()
	if pathTermMatches("internal/author/model.go", "auth") {
		t.Fatal("bare term auth must not match author")
	}
	if !pathTermMatches("internal/auth/model.go", "auth") {
		t.Fatal("auth must match auth directory")
	}
	if !pathTermMatches("api/v2/endpoint", "api/v2") {
		t.Fatal("punctuated terms must match by substring")
	}
}

func TestCapReasonsAppendsSentinel(t *testing.T) {
	t.Parallel()
	reasons := make([]string, maxReasons+3)
	for i := range reasons {
		reasons[i] = "r"
	}
	got := capReasons(reasons)
	if len(got) != maxReasons+1 || got[len(got)-1] != "... (3 more signals)" {
		t.Fatalf("capReasons = %v", got)
	}
	if got := capReasons([]string{"a", "b"}); len(got) != 2 {
		t.Fatalf("short reasons must pass through: %v", got)
	}
}

func TestSplitNUL(t *testing.T) {
	t.Parallel()
	if got := splitNUL(""); got != nil {
		t.Fatalf("splitNUL(\"\") = %v, want nil", got)
	}
	got := splitNUL("a.go\x00b.go\x00")
	if len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Fatalf("splitNUL = %v", got)
	}
}

func TestCapListKeepsTotalVisible(t *testing.T) {
	t.Parallel()
	paths := make([]string, maxHygieneListed+2)
	for i := range paths {
		paths[i] = "f.go"
	}
	got := capList(paths)
	if len(got) != maxHygieneListed+1 || got[len(got)-1] != "... (2 more)" {
		t.Fatalf("capList = %v", got[len(got)-1])
	}
}

func TestReadLinesReportsLineBound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "big.go")
	content := strings.Repeat("line\n", maxScanLines+5)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, truncated, err := readLines(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != maxScanLines || !truncated {
		t.Fatalf("readLines = %d lines, truncated=%t", len(lines), truncated)
	}
}

// TestChangeLogLiteralsMatchBounds pins the literal git arguments to the
// constants they must stay in sync with.
func TestChangeLogLiteralsMatchBounds(t *testing.T) {
	t.Parallel()
	if want := "--max-count=" + strconv.Itoa(maxChangeCommits); changeLogWalkBound != want {
		t.Fatalf("changeLogWalkBound = %q, want %q; keep it in sync with maxChangeCommits", changeLogWalkBound, want)
	}
	if got := strings.Count(changeLogFormat, string(commitFieldSep)); got != 2 {
		t.Fatalf("changeLogFormat separator count = %d, want 2", got)
	}
}
