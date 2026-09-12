package improve

import (
	"strings"
	"testing"
)

func TestRecommendationUsesCoverageAndStrategyHistory(t *testing.T) {
	t.Parallel()
	sufficient := Record{RunType: RunTypePrep, Outcome: "already_sufficient", Prep: &PrepContext{AlreadySufficient: true}}
	lift := Record{RunType: RunTypePrep, Success: true, Prep: &PrepContext{CoverageIncreased: true}}
	success := Record{RunType: RunTypeRefactor, Success: true, NetReduction: 10}
	tests := []struct {
		name    string
		records []Record
		task    string
	}{
		{name: "empty", task: TaskPrep},
		{name: "no prep", records: []Record{success}, task: TaskPrep},
		{name: "coverage failure", records: []Record{{RunType: RunTypePrep, Prep: &PrepContext{}}}, task: TaskPrep},
		{name: "covered", records: []Record{sufficient}, task: TaskDeadcode},
		{name: "lift", records: []Record{lift}, task: TaskDeadcode},
		{name: "successful refactor", records: []Record{sufficient, success}, task: TaskRefactor},
		{name: "gate failure", records: []Record{sufficient, success, {RunType: RunTypeRefactor, Outcome: string(OutcomePostTests)}}, task: TaskDeadcode},
		{name: "other failure", records: []Record{sufficient, {RunType: RunTypeRefactor, Outcome: "proposal_error"}}, task: TaskDeadcode},
		{name: "plateau", records: []Record{sufficient, success, success, success, success, success}, task: TaskDeadcode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			task, reason := recommendTask(tt.records, DefaultRecentWindow)
			if task != tt.task || reason == "" {
				t.Fatalf("recommendTask = %s, %s; want %s", task, reason, tt.task)
			}
		})
	}
}

func TestRunRecommendFiltersStrategyAndBoundsCandidates(t *testing.T) {
	t.Parallel()
	repo := newDeadCodeFixture(t)
	state := t.TempDir()
	history := NewHistory(HistoryPath(state))
	for _, record := range []Record{
		{RepoPath: repo, StrategyID: "old", RunType: RunTypeRefactor, Success: true},
		{RepoPath: repo, StrategyID: "current", RunType: RunTypePrep, Outcome: "already_sufficient"},
		{RepoPath: t.TempDir(), StrategyID: "current", RunType: RunTypeRefactor, Success: true},
	} {
		if err := history.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	report, err := RunRecommend(RecommendOptions{RepoPath: repo, StateDir: state, StrategyID: "current", RecentWindow: 10, MaxTargets: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Task != TaskDeadcode || report.History.TotalRuns != 1 || report.DeadSymbols != 1 || len(report.Candidates) != 1 {
		t.Fatalf("recommendation = %+v", report)
	}
	if !strings.Contains(report.Command, "--deadcode --dry-run") {
		t.Fatalf("command = %s", report.Command)
	}
}

func TestRecommendationCommandQuotesPaths(t *testing.T) {
	t.Parallel()
	command := recommendationCommand(RecommendOptions{RepoPath: "/tmp/a b'c", ConfigPath: "/tmp/strategy config.yaml"}, TaskRefactor)
	want := "pan --repo '/tmp/a b'\\''c' improve refactor --dry-run --config '/tmp/strategy config.yaml'"
	if command != want {
		t.Fatalf("command = %q; want %q", command, want)
	}
}
