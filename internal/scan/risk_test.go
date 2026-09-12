package scan

import (
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestRiskEntrypointPathTermAndOrdering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"cmd/app/main.go":  "package main\nfunc main() {}\n",
		"auth/token.go":    "package auth\nfunc Issue() {}\n",
		"plain/service.go": "package service\nfunc Ok() {}\n",
	})
	snap := goSnapshot(dir, "cmd/app/main.go", "auth/token.go", "plain/service.go")
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 2 {
		t.Fatalf("files = %v, want main.go and token.go only", report.Files)
	}
	if report.Files[0].Path != "auth/token.go" || !slices.Contains(report.Files[0].Lanes, "security") {
		t.Fatalf("top file = %+v, want token security finding", report.Files[0])
	}
	if report.Files[1].Path != "cmd/app/main.go" || report.Files[1].Score != 20 {
		t.Fatalf("entry file = %+v, want cmd/app/main.go scored 20", report.Files[1])
	}
	if report.FilesOmittedReason != "" {
		t.Fatalf("full list must not claim omission: %q", report.FilesOmittedReason)
	}
	laneNames := make([]string, 0, len(report.Lanes))
	for _, lane := range report.Lanes {
		laneNames = append(laneNames, lane.Name)
	}
	if !slices.Contains(laneNames, "cli-ux") || !slices.Contains(laneNames, "security") {
		t.Fatalf("lanes = %v", laneNames)
	}
}

func TestRiskTopTruncatesFilesKeepsLanes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"cmd/app/main.go": "package main\nfunc main() {}\n",
		"auth/token.go":   "package auth\nfunc A() {}\n",
	})
	snap := goSnapshot(dir, "cmd/app/main.go", "auth/token.go")
	report, err := Risk(t.Context(), snap, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("top=1 must keep one file, got %d", len(report.Files))
	}
	if len(report.Lanes) != 2 {
		t.Fatalf("lanes must reflect every scored file: %v", report.Lanes)
	}
	if report.FilesOmittedReason == "" {
		t.Fatal("truncation must be explained")
	}
}

func TestRiskContentPatternsAndLanes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"internal/db/db.go": "package db\nfunc Open() {\n\tsql.Open(\"x\", \"y\")\n\tpanic(\"boom\")\n}\n",
	})
	snap := goSnapshot(dir, "internal/db/db.go")
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("files = %+v", report.Files)
	}
	risk := report.Files[0]
	// sql.Open (database boundary, 10) + panic (4) = 14.
	if risk.Score != 14 {
		t.Fatalf("score = %d, want 14 (reasons: %v)", risk.Score, risk.Reasons)
	}
	if !slices.Contains(risk.Lanes, "data-integrity") || !slices.Contains(risk.Lanes, "error-handling") {
		t.Fatalf("lanes = %v", risk.Lanes)
	}
}

func TestRiskExcludesTestAndGeneratedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"auth/token_test.go": "package auth\nfunc TestX() {}\n",
		"gen/code.go":        "package gen\nfunc Iss() {}\n",
	})
	snap := analyzeFiles(
		analyze.File{Path: "auth/token_test.go", Language: languageGo},
		analyze.File{Path: "gen/code.go", Language: languageGo, Generated: true},
	)
	snap.Root = dir
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 0 || report.FilesOmittedReason == "" {
		t.Fatalf("test and generated files must be excluded: %+v", report)
	}
}

func TestRiskAddsOnlyGenericContentSignalsForParsedNonGoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"service.py": "# TODO: bound this\nhttp.ListenAndServe(\":8080\", nil)\n"})
	snap := analyzeFiles(analyze.File{Path: "service.py", Language: "python"})
	snap.Root = dir
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0].Score != 4 || !slices.Contains(report.Files[0].Lanes, laneBestPractices) {
		t.Fatalf("non-Go content risk = %#v", report.Files)
	}
	if len(report.Files[0].ScoreComponents) != 1 || report.Files[0].ScoreComponents[0].Reason != "change marker" {
		t.Fatalf("non-Go score components = %#v", report.Files[0].ScoreComponents)
	}
}

func TestRiskAddsComplexExportedSignatureEvidence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"api.go": "package api\n"})
	snap := analyzeFiles(analyze.File{Path: "api.go", Language: languageGo})
	snap.Root = dir
	snap.Symbols = []analyze.Symbol{{Name: "Serve", Exported: true, Signature: "(" + strings.Repeat("string, ", 12) + ") error", Location: analyze.Location{Path: "api.go"}}}
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || !slices.Contains(report.Files[0].Lanes, laneAPIContracts) || !slices.Contains(report.Files[0].Reasons, "1 complex public signature(s)") {
		t.Fatalf("signature risk = %#v", report.Files)
	}
}

func TestRiskProductionDefaultAndAdditiveClasses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"service.go":           "package service\n// TODO: inspect\n",
		"testdata/record.go":   "package record\n// TODO: fixture\n",
		"receipts/result.json": `{"url":"http://127.0.0.1"}`,
	})
	snap := analyzeFiles(
		analyze.File{Path: "service.go", Language: languageGo},
		analyze.File{Path: "testdata/record.go", Language: languageGo},
		analyze.File{Path: "receipts/result.json", Language: "json"},
	)
	snap.Root = dir
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0].Path != "service.go" {
		t.Fatalf("default files = %#v", report.Files)
	}
	all, err := RiskWithOptions(t.Context(), snap, 0, RiskOptions{IncludeClasses: []string{"fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Files) != 2 {
		t.Fatalf("additive fixture files = %#v", all.Files)
	}
	if all.Analysis.EligibleFiles != 2 || all.Analysis.ScoredFiles != 2 {
		t.Fatalf("coverage = %#v", all.Analysis)
	}
}

func TestRiskScoreComponentsCarryLocations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"service.go": "package service\n// TODO one\n// TODO two\n"})
	snap := analyzeFiles(analyze.File{Path: "service.go", Language: languageGo})
	snap.Root = dir
	report, err := Risk(t.Context(), snap, 0)
	if err != nil || len(report.Files) != 1 {
		t.Fatalf("report = %#v, err=%v", report, err)
	}
	if len(report.Files[0].ScoreComponents) == 0 || len(report.Files[0].ScoreComponents[0].Locations) == 0 {
		t.Fatalf("components = %#v", report.Files[0].ScoreComponents)
	}
	component := report.Files[0].ScoreComponents[0]
	if len(component.Locations) != len(component.Evidence) || component.Evidence[0] != "// TODO one" {
		t.Fatalf("component evidence = %#v, want paired source text", component)
	}
}

func TestRiskUnboundedReadExcludesDirectLimitReaderWrap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"internal/scanio/bounded.go": "package scanio\n" +
			"import \"io\"\n" +
			"func Bounded(r io.Reader) ([]byte, error) {\n" +
			"\treturn io.ReadAll( io.LimitReader(r, 1<<20))\n" +
			"}\n",
		"internal/scanio/plain.go": "package scanio\n" +
			"import \"io\"\n" +
			"func Plain(r io.Reader) ([]byte, error) {\n" +
			"\treturn io.ReadAll(r)\n" +
			"}\n",
	})
	snap := goSnapshot(dir, "internal/scanio/bounded.go", "internal/scanio/plain.go")
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0].Path != "internal/scanio/plain.go" {
		t.Fatalf("files = %#v, want only plain.go scored; the LimitReader-wrapped file must score 0", report.Files)
	}
	risk := report.Files[0]
	if risk.Score != 6 || !slices.Contains(risk.Lanes, lanePerformance) {
		t.Fatalf("plain ReadAll risk = %#v, want score 6 in the performance lane", risk)
	}
	if len(risk.ScoreComponents) != 1 {
		t.Fatalf("components = %#v", risk.ScoreComponents)
	}
	component := risk.ScoreComponents[0]
	if component.Reason != "unbounded read candidate" || component.Points != 6 || component.TotalMatches != 1 {
		t.Fatalf("component = %#v, want one full-weight unbounded-read match", component)
	}
}

func TestRiskUnboundedReadStaysFlaggedWhenNotDirectlyLimited(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"internal/scanio/ambiguous.go": "package scanio\n" +
			"import \"io\"\n" +
			"func Multiline(r io.Reader) ([]byte, error) {\n" +
			"\treturn io.ReadAll(\n" +
			"\t\tio.LimitReader(r, 1<<20),\n" +
			"\t)\n" +
			"}\n" +
			"func Mixed(r io.Reader) ([]byte, error) {\n" +
			"\t_, _ = io.ReadAll(io.LimitReader(r, 1<<20)), io.ReadAll(r)\n" +
			"\treturn nil, nil\n" +
			"}\n",
	})
	snap := goSnapshot(dir, "internal/scanio/ambiguous.go")
	report, err := Risk(t.Context(), snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 {
		t.Fatalf("files = %#v, want the ambiguous file scored", report.Files)
	}
	risk := report.Files[0]
	// The multiline ReadAll opener and the mixed line each stay flagged:
	// 6 + 6>>1 = 9 with unchanged score semantics.
	if risk.Score != 9 {
		t.Fatalf("score = %d, want 9 (reasons: %v)", risk.Score, risk.Reasons)
	}
	for _, component := range risk.ScoreComponents {
		if component.Reason != "unbounded read candidate" {
			t.Fatalf("unexpected component: %#v", component)
		}
		if component.TotalMatches != 2 || component.CountedMatches != 2 || len(component.Locations) != 2 {
			t.Fatalf("component = %#v, want both non-excluded lines counted with evidence", component)
		}
	}
}

func TestRiskSourceEvidenceCapsMatchesAndText(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	long := "// TODO " + strings.Repeat("é", maxRiskEvidenceRunes)
	writeTree(t, dir, map[string]string{"service.go": long + "\n// TODO two\n// TODO three\n// TODO four\n"})
	snap := analyzeFiles(analyze.File{Path: "service.go", Language: languageGo})
	snap.Root = dir
	report, err := Risk(t.Context(), snap, 0)
	if err != nil || len(report.Files) != 1 {
		t.Fatalf("report = %#v, err=%v", report, err)
	}
	for _, component := range report.Files[0].ScoreComponents {
		if component.Reason != "change marker" {
			continue
		}
		if len(component.Locations) != 3 || len(component.Evidence) != 3 {
			t.Fatalf("component = %#v, want three paired matches", component)
		}
		if component.Evidence[0] != string([]rune(long)[:maxRiskEvidenceRunes-3])+"..." {
			t.Fatalf("long evidence length/text mismatch: runes=%d text=%q", len([]rune(component.Evidence[0])), component.Evidence[0])
		}
		if component.Evidence[1] != "// TODO two" || component.Evidence[2] != "// TODO three" {
			t.Fatalf("evidence = %#v", component.Evidence)
		}
		return
	}
	t.Fatalf("change marker component missing: %#v", report.Files[0].ScoreComponents)
}
