package scan

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestEffectsKindsLanesAndCounts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"internal/work/work.go": strings.Join([]string{
			"package work",
			"func run() {",
			"\tos.WriteFile(\"p\", nil, 0o600)",
			"\thttp.Get(\"https://example.com\")",
			"\tgo func() {}()",
			"\tpassword := os.Getenv(\"PW\")",
			"}",
		}, "\n"),
	})
	snap := goSnapshot(dir, "internal/work/work.go")
	report, err := Effects(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("files = %+v", report.Files)
	}
	ef := report.Files[0]
	wantLanes := []string{"api-contracts", "data-integrity", "lifecycle-concurrency", "security"}
	if !slices.Equal(ef.Lanes, wantLanes) {
		t.Fatalf("lanes = %v, want %v", ef.Lanes, wantLanes)
	}
	kindNames := make([]string, 0, len(report.Kinds))
	for _, kind := range report.Kinds {
		kindNames = append(kindNames, kind.Name)
		if !slices.Contains(kind.Files, "internal/work/work.go") {
			t.Fatalf("kind %s must list its file: %+v", kind.Name, kind.Files)
		}
	}
	for _, want := range []string{kindFilesystemWrite, kindHTTP, kindGoroutine, kindSecret} {
		if !slices.Contains(kindNames, want) {
			t.Fatalf("kinds = %v, want %s", kindNames, want)
		}
	}
}

func TestEffectsPerFileCapRecordsTruncation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var lines []string
	for i := 0; i < perFileHitCap+2; i++ {
		lines = append(lines, "func f"+strconv.Itoa(i)+"() { os.Open(\"p\") }")
	}
	writeTree(t, dir, map[string]string{"a.go": strings.Join(lines, "\n")})
	snap := goSnapshot(dir, "a.go")
	report, err := Effects(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || len(report.Files[0].Effects) != perFileHitCap {
		t.Fatalf("effects must cap at %d: %+v", perFileHitCap, report.Files)
	}
	found := false
	for _, trunc := range report.Truncations {
		if trunc.Field == "files[a.go].effects" {
			found = true
		}
	}
	if !found {
		t.Fatalf("truncations = %+v", report.Truncations)
	}
}

func TestEffectsExcludesTests(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a_test.go": "package a\nfunc f() { os.Open(\"p\") }\n",
	})
	snap := goSnapshot(dir, "a_test.go")
	report, err := Effects(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 0 || report.FilesOmittedReason == "" {
		t.Fatalf("test files must be excluded: %+v", report)
	}
}

func TestEffectsWithOptionsFiltersBeforePerFileCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var lines []string
	for i := 0; i < perFileHitCap+1; i++ {
		lines = append(lines, "func read"+strconv.Itoa(i)+"() { os.Open(\"p\") }")
	}
	lines = append(lines, "func request() { http.Get(\"https://example.test\") }")
	writeTree(t, dir, map[string]string{"effects.go": strings.Join(lines, "\n")})

	report, err := EffectsWithOptions(t.Context(), goSnapshot(dir, "effects.go"), EffectsOptions{Kind: kindHTTP})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || len(report.Files[0].Effects) != 1 || report.Files[0].Effects[0].Kind != kindHTTP {
		t.Fatalf("kind filtering must precede the cap: %+v", report)
	}
	if got := report.EffectPaths(); !slices.Equal(got, []string{"effects.go"}) {
		t.Fatalf("paths = %v", got)
	}
}

func TestEffectsWithOptionsSupportsParsedLanguages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"client.py": "fetch('https://example.test')\n"})
	snap := analyze.Snapshot{Root: dir, Files: []analyze.File{{Path: "client.py", Language: "python"}}}
	report, err := EffectsWithOptions(t.Context(), snap, EffectsOptions{Language: "python"})
	if err != nil || len(report.Files) != 1 || report.Files[0].Effects[0].Kind != kindHTTP {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
}

func TestEffectsWithOptionsIgnoresCallsInComments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"client.py": "# fetch('https://example.test')\nvalue = 1\n"})
	snap := analyze.Snapshot{Root: dir, Files: []analyze.File{{Path: "client.py", Language: "python"}}}
	report, err := EffectsWithOptions(t.Context(), snap, EffectsOptions{Language: languagePython})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 0 {
		t.Fatalf("comment must not produce effect evidence: %+v", report)
	}
}

func TestEffectsWithOptionsRejectsUnknownLanguage(t *testing.T) {
	t.Parallel()
	_, err := EffectsWithOptions(t.Context(), analyze.Snapshot{}, EffectsOptions{Language: "fortran"})
	if err == nil || !strings.Contains(err.Error(), "unsupported effects language") {
		t.Fatalf("error = %v", err)
	}
}

func TestEffectsAnnotateGoASTEvidence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"effects.go": "package effects\nfunc write() { os.WriteFile(\"p\", nil, 0o600) }\n"})
	report, err := Effects(t.Context(), goSnapshot(dir, "effects.go"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("files = %+v", report.Files)
	}
	file := report.Files[0]
	if file.ID != "pan:effect:effects-go" || file.Score != 10 || file.Confidence != "medium" {
		t.Fatalf("file provenance = %+v", file)
	}
	if len(file.Effects) != 1 {
		t.Fatalf("effects = %+v", file.Effects)
	}
	effect := file.Effects[0]
	if effect.Op != "os.WriteFile" || effect.Path != "effects.go" || effect.Lane != laneDataIntegrity || effect.Provenance != "go_ast_call" || effect.Evidence != "parsed call expression" {
		t.Fatalf("effect = %+v", effect)
	}
	if got := EffectBoundaries(report, "effects.go"); !slices.Equal(got, []string{"Filesystem"}) {
		t.Fatalf("boundaries = %v", got)
	}
}
