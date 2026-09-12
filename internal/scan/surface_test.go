package scan

import (
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestSurfaceKongCommandsFlagsAndPatterns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"internal/cli/root.go": strings.Join([]string{
			"type Root struct {",
			"\tRepo string `name:\"repo\" default:\".\" help:\"Target repository root.\"`",
			"\tScan ScanCmd `cmd:\"\" help:\"Scan.\"`",
			"\tHidden string `hidden:\"\"`",
			"}",
			"func env() string { return os.Getenv(\"PAN_REPO\") }",
			"func route() { http.HandleFunc(\"/x\", handler) }",
		}, "\n"),
	})
	snap := goSnapshot(dir, "internal/cli/root.go")
	report, err := Surface(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("files = %+v", report.Files)
	}
	hits := report.Files[0].Hits
	names := map[string][]string{}
	for _, hit := range hits {
		names[hit.Kind] = append(names[hit.Kind], hit.Name)
	}
	if !slices.Contains(names["command"], "scan") {
		t.Fatalf("kong cmd tag must surface a command, got %+v", names)
	}
	if !slices.Contains(names["flag"], "repo") {
		t.Fatalf("kong flag tag must surface flag repo, got %+v", names)
	}
	if slices.Contains(names["flag"], "hidden") {
		t.Fatalf("hidden fields must not surface, got %+v", names)
	}
	if !slices.Contains(names["env-var"], "PAN_REPO") {
		t.Fatalf("env var must surface, got %+v", names)
	}
	if !slices.Contains(names["route"], "/x") {
		t.Fatalf("route must surface, got %+v", names)
	}
	if report.Counts["flag"] < 1 || report.Counts["command"] != 1 {
		t.Fatalf("counts = %+v", report.Counts)
	}
}

func TestSurfacePerFileCapRecordsTruncation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var lines []string
	for i := 0; i < perFileHitCap+3; i++ {
		lines = append(lines, "func f() { os.Getenv(\"V\"+strconv.Itoa(i)) }")
	}
	writeTree(t, dir, map[string]string{"a.go": strings.Join(lines, "\n")})
	snap := goSnapshot(dir, "a.go")
	report, err := Surface(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || len(report.Files[0].Hits) != perFileHitCap {
		t.Fatalf("hits must cap at %d: %+v", perFileHitCap, report.Files)
	}
	if report.Counts["env-var"] != perFileHitCap+3 {
		t.Fatalf("counts must reflect every hit: %+v", report.Counts)
	}
	found := false
	for _, trunc := range report.Truncations {
		if trunc.Field == "files[a.go].hits" && trunc.Total == perFileHitCap+3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("truncations = %+v", report.Truncations)
	}
}

func TestSurfaceExcludesTestsAndUnknownFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a_test.go": "package a\nfunc f() { os.Getenv(\"X\") }\n",
		"notes.md":  "os.Getenv(\"X\")",
	})
	snap := analyze.Snapshot{Root: dir, Files: []analyze.File{
		{Path: "a_test.go", Language: languageGo},
		{Path: "notes.md", Language: "unknown"},
	}}
	report, err := Surface(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 0 || report.FilesOmittedReason == "" {
		t.Fatalf("only parsed non-test source files are inspected: %+v", report)
	}
}

func TestSurfaceRecognizesParsedNonGoSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"handler.ts": `const key = process.env.API_KEY`})
	snap := analyze.Snapshot{Root: dir, Files: []analyze.File{{Path: "handler.ts", Language: "typescript"}}}
	report, err := Surface(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 0 {
		t.Fatalf("unrecognized JavaScript environment shape must not be guessed: %+v", report)
	}
}

func TestSurfaceTopTruncatesFileList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.go": "package a\nfunc f() { os.Getenv(\"A\") }\n",
		"b.go": "package b\nfunc f() { os.Getenv(\"B\") }\n",
	})
	snap := goSnapshot(dir, "a.go", "b.go")
	report, err := Surface(t.Context(), snap, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.FilesOmittedReason == "" {
		t.Fatalf("top=1 must truncate: %+v", report)
	}
}
