package improve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedTestGenerator struct {
	calls int
}

func (g *scriptedTestGenerator) Generate(_ context.Context, _ PrepTarget, feedback string) ([]FileChange, error) {
	g.calls++
	if g.calls == 1 {
		return []FileChange{{FilePath: "bad.go", NewContents: "package sample"}}, nil
	}
	if feedback == "" {
		return nil, nil
	}
	return []FileChange{{FilePath: "demo_test.go", NewContents: "package sample\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal() } }\n"}}, nil
}

func TestPrepGeneratorRetriesRejectedOutputInDisposableFixture(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	writeFile(t, dir, "demo.go", "package sample\n\nfunc Value() int { return 1 }\nfunc Uncovered() int { return 2 }\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	generator := &scriptedTestGenerator{}
	report, err := RunPrep(context.Background(), PrepOptions{
		RepoPath: dir, StateDir: t.TempDir(), Floor: 1, MaxTargets: 1, MaxRetries: 1, Generator: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempts != 2 || len(report.GeneratedFiles) != 1 {
		t.Fatalf("attempts=%d generated=%v", report.Attempts, report.GeneratedFiles)
	}
	if !report.Success || report.Outcome != string(OutcomeSuccess) {
		t.Fatalf("unexpected report: %+v", report)
	}
}

type completeTestGenerator struct{}

func (completeTestGenerator) Generate(context.Context, PrepTarget, string) ([]FileChange, error) {
	return []FileChange{{FilePath: "demo_test.go", NewContents: "package sample\n\nimport \"testing\"\n\nfunc TestValues(t *testing.T) { if Value() != 1 || Uncovered() != 2 { t.Fatal() } }\n"}}, nil
}

func TestPrepGeneratorLiveSettlesDisposableFixture(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	writeFile(t, dir, "demo.go", "package sample\n\nfunc Value() int { return 1 }\nfunc Uncovered() int { return 2 }\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	report, err := RunPrep(context.Background(), PrepOptions{RepoPath: dir, StateDir: t.TempDir(), Floor: 1, MaxTargets: 1, Live: true, Generator: completeTestGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.DryRun || report.Outcome != string(OutcomeSuccess) {
		t.Fatalf("live prep report = %+v", report)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "demo_test.go")); !os.IsNotExist(statErr) {
		t.Fatalf("live prep changed caller checkout: %v", statErr)
	}
	branches, err := gitOutput(context.Background(), dir, "branch", "--list", "pan-improve-*")
	if err != nil || strings.TrimSpace(branches) == "" {
		t.Fatalf("live prep attempt branch missing: %q, %v", branches, err)
	}
}

func TestPrepGenerationHonorsMaxFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	writeFile(t, dir, "demo.go", "package sample\n\nfunc Value() int { return 1 }\nfunc Other() int { return 2 }\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	report, err := RunPrep(context.Background(), PrepOptions{RepoPath: dir, StateDir: t.TempDir(), Floor: 1, MaxTargets: 2, MaxFiles: 1, ContinueToFloor: true, Generator: completeTestGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.GeneratedFiles) > 1 {
		t.Fatalf("generated files = %v, want max one", report.GeneratedFiles)
	}
}

type roundTestGenerator struct{ calls int }

func (g *roundTestGenerator) Generate(context.Context, PrepTarget, string) ([]FileChange, error) {
	g.calls++
	if g.calls == 1 {
		return nil, nil
	}
	return completeTestGenerator{}.Generate(context.Background(), PrepTarget{}, "")
}

func TestPrepRetriesTargetInLaterRound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	writeFile(t, dir, "demo.go", "package sample\n\nfunc Value() int { return 1 }\nfunc Uncovered() int { return 2 }\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	generator := &roundTestGenerator{}
	report, err := RunPrep(context.Background(), PrepOptions{RepoPath: dir, StateDir: t.TempDir(), Floor: 1, MaxTargets: 1, TargetRounds: 2, Generator: generator})
	if err != nil {
		t.Fatal(err)
	}
	if generator.calls != 2 || len(report.GeneratedFiles) != 1 {
		t.Fatalf("calls=%d files=%v", generator.calls, report.GeneratedFiles)
	}
	if len(report.TargetOutcomes) != 2 || report.TargetOutcomes[0].Round != 1 || report.TargetOutcomes[1].Round != 2 {
		t.Fatalf("outcomes=%+v", report.TargetOutcomes)
	}
}

func TestPrepWritesBoundedFailureArtifact(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	writeFile(t, dir, "demo.go", "package sample\n\nfunc Value() int { return 1 }\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	report, err := RunPrep(context.Background(), PrepOptions{RepoPath: dir, StateDir: state, Floor: 1, MaxTargets: 1, Generator: &scriptedTestGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FailureArtifacts) != 1 || !strings.HasPrefix(report.FailureArtifacts[0], filepath.Join(state, "failures", "prep")) {
		t.Fatalf("artifacts=%v", report.FailureArtifacts)
	}
	data, err := os.ReadFile(report.FailureArtifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || strings.Contains(string(data), "package sample") {
		t.Fatalf("artifact leaks generated source: %q", data)
	}
}

type partialTestGenerator struct{}

func (partialTestGenerator) Generate(context.Context, PrepTarget, string) ([]FileChange, error) {
	return []FileChange{{FilePath: "demo_test.go", NewContents: "package sample\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal() } }\n"}}, nil
}

func TestPrepLiveSettlesCoverageGainBelowFloor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	writeFile(t, dir, "demo.go", "package sample\n\nfunc Value() int { return 1 }\nfunc Uncovered() int { return 2 }\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	report, err := RunPrep(context.Background(), PrepOptions{RepoPath: dir, StateDir: t.TempDir(), Floor: 1, MaxTargets: 1, Live: true, Generator: partialTestGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Outcome != string(OutcomeSuccess) || report.FinalCoverage >= 100 {
		t.Fatalf("report=%+v", report)
	}
	branches, err := gitOutput(context.Background(), dir, "branch", "--list", "pan-improve-*")
	if err != nil || strings.TrimSpace(branches) == "" {
		t.Fatalf("attempt branch missing: %q, %v", branches, err)
	}
}

func TestPrepBatchFullSuiteSettlesProvisionalAcceptance(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	writeFile(t, dir, "demo.go", "package sample\n\nfunc Value() int { return 1 }\nfunc Uncovered() int { return 2 }\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	report, err := RunPrep(context.Background(), PrepOptions{RepoPath: dir, StateDir: t.TempDir(), Floor: 1, MaxTargets: 1, BatchFullSuite: true, Generator: completeTestGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.FinalCoverage < 100 || len(report.TargetOutcomes) != 1 || report.TargetOutcomes[0].Provisional {
		t.Fatalf("report=%+v", report)
	}
}

type concurrentPrepGenerator struct {
	mu      sync.Mutex
	active  int
	maximum int
}

func (g *concurrentPrepGenerator) Generate(context.Context, PrepTarget, string) ([]FileChange, error) {
	g.mu.Lock()
	g.active++
	if g.active > g.maximum {
		g.maximum = g.active
	}
	g.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	g.mu.Lock()
	g.active--
	g.mu.Unlock()
	return nil, nil
}

func TestPrepGenerationRoundBoundsConcurrentFirstPass(t *testing.T) {
	generator := &concurrentPrepGenerator{}
	targets := []PrepTarget{{File: "a.go"}, {File: "b.go"}, {File: "c.go"}}
	selected, generated := prepGenerationRound(context.Background(), targets, nil, 0, PrepOptions{Generator: generator, ContinueToFloor: true, MaxConcurrent: 2})
	if len(selected) != 3 || len(generated) != 3 || generator.maximum != 2 {
		t.Fatalf("selected=%d generated=%d max=%d", len(selected), len(generated), generator.maximum)
	}
	for _, item := range generated {
		if item == nil || item.err != nil {
			t.Fatalf("generation=%+v", item)
		}
	}
}

func TestPrepRequestPacingCancellationEndsTarget(t *testing.T) {
	state := newGeneratedPrepState(t.TempDir(), "HEAD", TestResult{}, PrepOptions{
		Generator:       completeTestGenerator{},
		RequestInterval: time.Hour,
	})
	state.lastRequest = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := state.runTarget(ctx, PrepTarget{File: "demo.go"}, 1, 1, nil)
	if !errors.Is(run.Err, context.Canceled) {
		t.Fatalf("run error = %v, want canceled pacing error", run.Err)
	}
	if state.result.Attempts != 1 {
		t.Fatalf("attempts = %d, want one unsent request", state.result.Attempts)
	}
}
