package improve

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFile is the shared fixture helper: name is slash-relative to dir.
func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const fixtureGoMod = "module probe.example/demo\n\ngo 1.25\n"

const fixtureWithDeadCode = `package demo

import "fmt"

// Used is the exported surface.
func Used() string { return fmt.Sprintf("%d", helper()) }

func helper() int { return 1 }

// deadHelper has zero references anywhere in the module.
func deadHelper() int { return 2 }
`

// newDeadCodeFixture returns a module with exactly one dead symbol:
// deadHelper.
func newDeadCodeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", fixtureGoMod)
	writeFile(t, dir, "demo.go", fixtureWithDeadCode)
	return dir
}

func TestValidateProposalRejectsUnsafeChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "src.go", "package demo\n\nfunc ok() {}\n")
	tests := []struct {
		name    string
		changes []FileChange
	}{
		{
			name:    "empty path",
			changes: []FileChange{{FilePath: "  ", NewContents: "package demo\n"}},
		},
		{
			name:    "absolute path",
			changes: []FileChange{{FilePath: filepath.Join(t.TempDir(), "escape.go"), NewContents: "package demo\n"}},
		},
		{
			name:    "path escapes root",
			changes: []FileChange{{FilePath: "../escape.go", NewContents: "package demo\n"}},
		},
		{
			name:    "path enters .git",
			changes: []FileChange{{FilePath: ".git/hooks/pre-commit", NewContents: "#!/bin/sh\n"}},
		},
		{
			name:    "test file",
			changes: []FileChange{{FilePath: "src_test.go", NewContents: "package demo\n"}},
		},
		{
			name:    "delete of missing file",
			changes: []FileChange{{FilePath: "missing.go"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateProposal(root, tt.changes); err == nil {
				t.Fatalf("ValidateProposal accepted unsafe change %q", tt.name)
			}
		})
	}
}

func TestValidateProposalAcceptsSafeChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "src.go", "package demo\n")
	if err := ValidateProposal(root, []FileChange{{FilePath: "src.go", NewContents: "package demo\n\nfunc f() {}\n"}}); err != nil {
		t.Fatalf("ValidateProposal rejected a safe change: %v", err)
	}
}

func TestApplyProposalWritesAndDeletesOnlyNamedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "keep.go", "package demo\n")
	changes := []FileChange{
		{FilePath: "inner/changed.go", NewContents: "package demo\n"},
		{FilePath: "keep.go"}, // empty contents: delete
	}
	if err := ApplyProposal(dir, changes); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir, "inner/changed.go"); got != "package demo\n" {
		t.Fatalf("written contents = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.go")); !os.IsNotExist(err) {
		t.Fatalf("keep.go should have been deleted, stat err = %v", err)
	}
}

func TestProbeNeverWritesTheTarget(t *testing.T) {
	t.Parallel()
	dir := newDeadCodeFixture(t)
	report, err := RunProbe(ProbeOptions{RepoPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Outcome != string(OutcomeSuccess) {
		t.Fatalf("probe outcome = %q success=%v reason=%q", report.Outcome, report.Success, report.Reason)
	}
	if report.Applied {
		t.Fatal("probe must never report applied=true")
	}
	if report.NetLineReduction <= 0 || report.LinesDeleted <= 0 {
		t.Fatalf("probe diff = +%d/-%d", report.LinesAdded, report.LinesDeleted)
	}
	if got := readFile(t, dir, "demo.go"); got != fixtureWithDeadCode {
		t.Fatal("probe mutated the target source file")
	}
}

func TestRunRecommendBanksCoverageBeforeDeadCode(t *testing.T) {
	t.Parallel()
	dir := newDeadCodeFixture(t)
	report, err := RunRecommend(RecommendOptions{RepoPath: dir, StateDir: t.TempDir(), Floor: 0.7, RecentWindow: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Task != TaskPrep || report.DeadSymbols != 0 || len(report.Candidates) != 0 {
		t.Fatalf("unexpected recommendation: %+v", report)
	}
	if !report.DryRunOnly {
		t.Fatal("recommendation must stay dry-run only")
	}
}

func TestRunRecommendFallsBackToPrep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", fixtureGoMod)
	writeFile(t, dir, "demo.go", "package demo\n\nfunc Used() string { return \"clean\" }\n")
	report, err := RunRecommend(RecommendOptions{RepoPath: dir, StateDir: t.TempDir(), Floor: 0.7})
	if err != nil {
		t.Fatal(err)
	}
	if report.Task != TaskPrep || report.DeadSymbols != 0 || len(report.Candidates) != 0 {
		t.Fatalf("unexpected recommendation: %+v", report)
	}
}

func TestRunStatsAggregatesRepoScopedHistory(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	history := NewHistory(HistoryPath(stateDir))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := Record{
		Timestamp: now, RunType: RunTypeRefactor, RepoPath: "/repos/demo",
		Success: true, Outcome: string(OutcomeSuccess), DryRun: true,
		NetReduction: 10, SymbolsDeleted: 2,
	}
	records := []Record{
		base,
		{Timestamp: now, RunType: RunTypeRefactor, RepoPath: "/repos/other", Success: true, Outcome: string(OutcomeSuccess)},
		{Timestamp: now, RunType: RunTypePrep, RepoPath: "/repos/demo", Success: true, Outcome: string(OutcomePlanned), DryRun: true},
	}
	for _, rec := range records {
		if err := history.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	report, err := RunStats(StatsOptions{RepoPath: "/repos/demo", StateDir: stateDir, RecentWindow: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Records != 2 || report.TotalRuns != 2 || report.Successes != 2 || report.RefactorRuns != 1 || report.PrepRuns != 0 {
		t.Fatalf("unexpected stats report: %+v", report)
	}
	if !report.DryRunOnly {
		t.Fatal("dry-run-only posture must hold while every record is a dry run")
	}
}

func TestHistoryRejectsCorruptLedger(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	writeFile(t, stateDir, "history.jsonl", "{not json\n")
	if _, err := RunStats(StatsOptions{RepoPath: "/repos/demo", StateDir: stateDir}); err == nil {
		t.Fatal("corrupt ledger must surface as an error, never as zero runs")
	}
}

func TestHistoryReadsSQLiteWhenJSONLIsAbsent(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	history := NewHistory(HistoryPath(stateDir))
	if err := history.Append(Record{RunType: RunTypeRefactor, RepoPath: "/repos/demo", Success: true, Outcome: string(OutcomeSuccess)}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(history.Path()); err != nil {
		t.Fatal(err)
	}
	records, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RepoPath != "/repos/demo" {
		t.Fatalf("SQLite recovery records = %+v", records)
	}
}

func TestRunStatsEmptyLedgerReadsAsZero(t *testing.T) {
	t.Parallel()
	report, err := RunStats(StatsOptions{RepoPath: "/repos/demo", StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if report.Records != 0 || report.TotalRuns != 0 || report.SuccessRate != 0 {
		t.Fatalf("empty ledger must read as zero runs: %+v", report)
	}
}

func TestTestIdentityGates(t *testing.T) {
	t.Parallel()
	baseline := TestResult{
		TotalTests: 2,
		Tests:      []string{"pkg\tTestA", "pkg\tTestB/sub"},
	}
	tests := []struct {
		name      string
		post      TestResult
		wantIdent bool
		wantSkip  bool
	}{
		{
			name: "unchanged passes",
			post: TestResult{TotalTests: 2, Tests: []string{"pkg\tTestA", "pkg\tTestB/sub"}},
		},
		{
			name:      "disappeared test is an identity violation",
			post:      TestResult{TotalTests: 1, Tests: []string{"pkg\tTestA"}},
			wantIdent: true,
		},
		{
			name:     "newly skipped test is a skip violation",
			post:     TestResult{TotalTests: 2, Tests: []string{"pkg\tTestA", "pkg\tTestB/sub"}, Skipped: []string{"pkg\tTestA"}},
			wantSkip: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertGateOutcome(t, baseline, tt.post, tt.wantIdent, tt.wantSkip)
		})
	}
}

func TestCoverageGateRejectsDrop(t *testing.T) {
	t.Parallel()
	reason := coverageGateReason(
		TestResult{Files: []FileCoverage{{File: "demo.go", Coverage: 80}}},
		TestResult{Files: []FileCoverage{{File: "demo.go", Coverage: 60}}},
		[]FileChange{{FilePath: "demo.go", NewContents: "package demo\n"}}, true, 5,
	)
	if reason == "" {
		t.Fatal("coverage drop passed gate")
	}
}

// assertGateOutcome checks one anti-cheat gate expectation against the
// identity reason for one post-change suite.
func assertGateOutcome(t *testing.T, baseline, post TestResult, wantIdent, wantSkip bool) {
	t.Helper()
	reason := testIdentityReason(baseline, post)
	switch {
	case wantIdent:
		if reason == "" || !missingBaselineTest(baseline, post) {
			t.Fatalf("want identity violation, got reason %q", reason)
		}
	case wantSkip:
		if reason == "" || missingBaselineTest(baseline, post) {
			t.Fatalf("want skip violation, got reason %q", reason)
		}
	case reason != "":
		t.Fatalf("gates must pass, got reason %q", reason)
	}
}

func TestRankPrepTargetsIsDeterministicAndBounded(t *testing.T) {
	t.Parallel()
	files := []FileCoverage{
		{File: "b/low.go", Coverage: 10, UncoveredStatements: 50},
		{File: "a/high.go", Coverage: 90, UncoveredStatements: 1},
		{File: "c/low.go", Coverage: 20, UncoveredStatements: 50},
		{File: "covered.go", Coverage: 100, UncoveredStatements: 0},
	}
	targets := rankPrepTargets(files, 70, 2, 100)
	if len(targets) != 2 {
		t.Fatalf("bound not applied: %d targets", len(targets))
	}
	if targets[0].File != "b/low.go" || targets[1].File != "c/low.go" {
		t.Fatalf("score order wrong (deficit-weighted, tie broken by path): %+v", targets)
	}
	if targets[0].EstimatedLiftPercent != 50 || targets[0].Deficit != 0.9 {
		t.Fatalf("unexpected scoring fields: %+v", targets[0])
	}
}

func TestCensusFromFilesRollup(t *testing.T) {
	t.Parallel()
	files := []FileCoverage{
		{File: "cmd/x/main.go", TotalStatements: 10, CoveredStatements: 5, UncoveredStatements: 5, Coverage: 50},
		{File: "cmd/x/util.go", TotalStatements: 10, CoveredStatements: 10, UncoveredStatements: 0, Coverage: 100},
		{File: "root.go", TotalStatements: 4, CoveredStatements: 0, UncoveredStatements: 4, Coverage: 0},
	}
	report := censusFromFiles(files)
	if !report.CoverageMeasured || report.Files != 3 {
		t.Fatalf("unexpected census header: %+v", report)
	}
	if len(report.Packages) != 2 {
		t.Fatalf("package rollup wrong: %+v", report.Packages)
	}
	if report.Packages[0].Package != "." || report.Packages[0].Coverage != 0 {
		t.Fatalf("root package wrong: %+v", report.Packages[0])
	}
	if report.Packages[1].Package != "cmd/x" || report.Packages[1].Coverage != 75 {
		t.Fatalf("cmd/x package wrong: %+v", report.Packages[1])
	}
	if report.TotalStatements != 24 || report.CoveredStatements != 15 || report.TotalCoverage != 62.5 {
		t.Fatalf("totals wrong: %+v", report)
	}
}

func TestRemoveDeclarationsKeepsTestFunctions(t *testing.T) {
	t.Parallel()
	src := []byte("package demo\n\nfunc dead() int { return 1 }\n\nfunc TestSomething(t *T) { _ = dead() }\n")
	_, dropped := removeDeclarations(src, map[string]bool{"dead": true, "TestSomething": true})
	for _, name := range dropped {
		if name == "TestSomething" {
			t.Fatal("test functions must never be removed by AST surgery")
		}
	}
}

func TestDiffLinesCountsMultisetDelta(t *testing.T) {
	t.Parallel()
	added, deleted := DiffLines("a\nb\n", "a\nc\n")
	if added != 1 || deleted != 1 {
		t.Fatalf("DiffLines = +%d/-%d, want +1/-1", added, deleted)
	}
}
