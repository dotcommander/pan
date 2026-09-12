package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// helpers

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func rankedFile(path string, syms []symbols.Symbol) symbols.RankedFile {
	fs := &symbols.FileSymbols{
		Path:     path,
		Language: "go",
		Symbols:  syms,
	}
	return symbols.RankedFile{
		FileSymbols: fs,
	}
}

func sym(name string) symbols.Symbol {
	return symbols.Symbol{Name: name, Kind: "func", Exported: true}
}

// checkExistence tests

func TestCheckExistence_Missing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "alpha", Files: []string{"internal/missing.go"}},
		},
	}

	findings := checkExistence(s, root)

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Check != "existence" {
		t.Errorf("check = %q, want %q", f.Check, "existence")
	}
	if f.Severity != High {
		t.Errorf("severity = %v, want High", f.Severity)
	}
	if f.Phase != "alpha" {
		t.Errorf("phase = %q, want %q", f.Phase, "alpha")
	}
}

func TestCheckExistence_Exists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "internal/present.go"), "package internal\n")

	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "alpha", Files: []string{"internal/present.go"}},
		},
	}

	findings := checkExistence(s, root)

	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got %d: %v", len(findings), findings)
	}
}

// checkPhaseOrphans tests

func TestCheckPhaseOrphans(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "beta", Files: []string{"a.go", "b.go"}},
		},
	}

	findings := checkPhaseOrphans(s, root)

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Check != "phase-orphan" {
		t.Errorf("check = %q, want %q", f.Check, "phase-orphan")
	}
	if f.Severity != High {
		t.Errorf("severity = %v, want High", f.Severity)
	}
	if !strings.Contains(f.Message, "2") {
		t.Errorf("message should mention file count: %q", f.Message)
	}
}

// checkSymbolCoverage tests

func TestCheckSymbolCoverage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realFile := filepath.Join(root, "internal/handler.go")
	mustWriteFile(t, realFile, "package internal\n")

	// Symbol index: HandleRequest lives in realFile.
	symIdx := map[string][]string{
		"HandleRequest": {realFile},
	}

	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name:  "gateway",
				Files: []string{"internal/other.go"}, // not the file where symbol lives
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "HandleRequest"}},
				},
			},
			{
				Name: "missing-sym",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "NonExistentFunc"}},
				},
			},
		},
	}

	findings := checkSymbolCoverage(s, root, symIdx)

	// Expect: NonExistentFunc not found (High), HandleRequest wrong phase (Medium).
	highCount := 0
	medCount := 0
	for _, f := range findings {
		switch f.Severity {
		case High:
			highCount++
		case Medium:
			medCount++
		case Info, Critical:
			// Not expected in this scenario; neither bucket counts.
		}
	}
	if highCount == 0 {
		t.Error("expected at least one High finding for missing symbol")
	}
	if medCount == 0 {
		t.Error("expected at least one Medium finding for wrong-phase symbol")
	}
}

func TestReviewNormalizesRelativeRepomapPaths(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc Run() {}\n")
	s := &spec.Spec{Phases: []spec.Phase{{
		Name:   "Run",
		Files:  []string{"main.go"},
		Stages: []spec.Stage{{Chip: &spec.Chip{Label: "Run"}}},
	}}}
	ranked := []symbols.RankedFile{rankedFile("main.go", []symbols.Symbol{sym("Run")})}

	report := Review(context.Background(), s, root, ranked)
	if len(report.Findings) != 0 {
		t.Fatalf("relative ranked paths produced findings: %v", report.Findings)
	}
}

func TestCheckSymbolCoverageIgnoresDescriptiveBranchLabels(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{Phases: []spec.Phase{{
		Name: "Route",
		Stages: []spec.Stage{{Fork: &spec.Fork{
			Gate:     "mode",
			Branches: []spec.Branch{{Condition: "default", Label: "Round"}},
		}}},
	}}}

	if findings := checkSymbolCoverage(s, t.TempDir(), nil); len(findings) != 0 {
		t.Fatalf("descriptive fork label treated as Go symbol: %v", findings)
	}
}

// checkStoreFreshness tests

func TestCheckStoreFreshness(t *testing.T) {
	t.Parallel()

	symIdx := map[string][]string{
		"WriteRecord": {"/some/file.go"},
	}

	s := &spec.Spec{
		Stores: []spec.Store{
			{
				Name: "eventlog",
				Writers: []spec.Writer{
					{Stage: "MissingSymbol"}, // exported ident, not in index
					{Stage: "WriteRecord"},   // present — no finding
				},
			},
		},
	}

	findings := checkStoreFreshness(s, symIdx)

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(findings), findings)
	}
	f := findings[0]
	if f.Check != "store-freshness" {
		t.Errorf("check = %q, want %q", f.Check, "store-freshness")
	}
	if f.Severity != Medium {
		t.Errorf("severity = %v, want Medium", f.Severity)
	}
	if !strings.Contains(f.Message, "MissingSymbol") {
		t.Errorf("message should mention symbol name: %q", f.Message)
	}
}

// checkCoverageGap tests

func TestCheckCoverageGap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	unrefFile := filepath.Join(root, "internal/unref.go")

	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "alpha", Files: []string{"internal/present.go"}},
		},
	}

	ranked := []symbols.RankedFile{
		rankedFile(unrefFile, []symbols.Symbol{sym("DoWork")}),
	}

	findings := checkCoverageGap(s, root, ranked)

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Check != "coverage-gap" {
		t.Errorf("check = %q, want %q", f.Check, "coverage-gap")
	}
	if f.Severity != Medium {
		t.Errorf("severity = %v, want Medium", f.Severity)
	}
}

func TestCheckCoverageGapDoesNotRepeatRecordedScanOmissions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	unrefFile := filepath.Join(root, "tools", "helper.go")
	s := &spec.Spec{Coverage: &spec.Coverage{
		Represented: 0,
		Total:       1,
		Missing:     []string{"tools/helper.go"},
	}}
	ranked := []symbols.RankedFile{rankedFile(unrefFile, []symbols.Symbol{sym("Run")})}

	if findings := checkCoverageGap(s, root, ranked); len(findings) != 0 {
		t.Fatalf("recorded scan omission repeated as drift: %v", findings)
	}
}

// Dispatcher-shape regression: phases live under s.Commands[i].Phases, not
// s.Phases. Pre-fix, checkCoverageGap iterated only s.Phases and flagged
// command-owned files as uncovered. Post-fix it iterates s.AllPhases().
func TestCheckCoverageGap_DispatcherShape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmdFile := filepath.Join(root, "internal/cmdfile.go")

	s := &spec.Spec{
		Commands: []spec.Command{
			{
				Name: "build",
				Phases: []spec.Phase{
					{Name: "compile", Files: []string{"internal/cmdfile.go"}},
				},
			},
		},
	}

	ranked := []symbols.RankedFile{
		rankedFile(cmdFile, []symbols.Symbol{sym("Build")}),
	}

	findings := checkCoverageGap(s, root, ranked)

	if len(findings) != 0 {
		t.Fatalf("expected 0 findings (file is covered by command phase), got %d: %v",
			len(findings), findings)
	}
}

// Report helper tests

func TestHasSeverity(t *testing.T) {
	t.Parallel()

	r := &Report{
		Findings: []Finding{
			{Severity: Medium},
		},
	}

	if !r.HasSeverity(Medium) {
		t.Error("HasSeverity(Medium) should be true")
	}
	if r.HasSeverity(High) {
		t.Error("HasSeverity(High) should be false")
	}
	if !r.HasSeverity(Info) {
		t.Error("HasSeverity(Info) should be true (Medium >= Info)")
	}
}

func TestReviewFlagsDeclaredLowCoverage(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{Coverage: &spec.Coverage{Represented: 1, Total: 2, Missing: []string{"missing.go"}}}
	report := Review(context.Background(), s, t.TempDir(), nil)
	if !report.HasSeverity(High) {
		t.Fatalf("low declared coverage did not produce a high finding: %#v", report.Findings)
	}
	if report.Findings[0].Check != "coverage-confidence" {
		t.Fatalf("check = %q, want coverage-confidence", report.Findings[0].Check)
	}
}

// Markdown test

func TestMarkdown(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "alpha", Files: []string{"missing.go"}},
		},
	}

	report := Review(context.Background(), s, root, nil)
	md := report.Markdown()

	for _, want := range []string{"## Critical", "## High", "## Medium", "## Info"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown missing section %q", want)
		}
	}
	if !strings.Contains(md, "finding(s)") {
		t.Error("Markdown missing summary line")
	}
}
