package improve

import (
	"testing"
	"time"
)

func TestAggregateSourceParityLanesAndPrepCoverage(t *testing.T) {
	t.Parallel()
	records := []Record{
		{RunType: RunTypeRefactor, Success: true, NetReduction: 3, FeeEarned: 1.5, Model: "one", Target: &TargetContext{RiskLanes: []string{"architecture"}}, Candidates: &CandidatePacket{Files: []CandidateFile{{Lane: "surface", Actionability: "inspect"}}, SkippedSignals: []string{"history"}}},
		{RunType: RunTypePrep, Success: true, Model: "one", Prep: &PrepContext{CoverageIncreased: true, Targets: []PrepTargetContext{{Outcome: "added"}}}},
		{RunType: RunTypePrep, Success: true, Model: "two", Prep: &PrepContext{AlreadySufficient: true}},
	}
	stats := Aggregate(records)
	if stats.TotalFees != 1.5 || stats.ByModel["one"] != 2 || stats.ByModel["two"] != 1 {
		t.Fatalf("fees/models = %v %#v", stats.TotalFees, stats.ByModel)
	}
	if stats.PrepRuns != 1 || stats.PrepCoverageIncreases != 1 || !stats.PrepCoverageTargetMet {
		t.Fatalf("prep = %+v", stats)
	}
	if stats.ByTargetRiskLane["architecture"] != 1 || stats.ByPrepTargetOutcome["added"] != 1 || stats.ByCandidateLane["surface"] != 1 || stats.ByActionability["inspect"] != 1 || stats.BySkippedSignal["history"] != 1 {
		t.Fatalf("lanes = %+v", stats)
	}
}

func TestAggregateNetReductionConfidence(t *testing.T) {
	t.Parallel()
	stats := Aggregate([]Record{{RunType: RunTypeRefactor, Success: true, NetReduction: 1}, {RunType: RunTypeRefactor, Success: true, NetReduction: 3}, {RunType: RunTypeRefactor, Success: true, NetReduction: 5}})
	if stats.NetReductionConfidence == nil || *stats.NetReductionConfidence <= 0 {
		t.Fatalf("confidence = %v", stats.NetReductionConfidence)
	}
}

func TestAggregateNoCandidateIsSuccessfulWithoutReductionSample(t *testing.T) {
	t.Parallel()
	stats := Aggregate([]Record{
		{RunType: RunTypeRefactor, Success: true, Outcome: string(OutcomeNoCandidate), NetReduction: 99, FeeEarned: 10},
		{RunType: RunTypeRefactor, Success: true, Outcome: string(OutcomeSuccess), NetReduction: 3, FeeEarned: 1.5},
	})
	if stats.Successes != 2 || stats.TotalFees != 1.5 || stats.AvgNetReduction != 3 || stats.BestNetReduction != 3 {
		t.Fatalf("no-candidate stats = %+v", stats)
	}
}

func TestAggregateUsesStructuralPayoutReduction(t *testing.T) {
	t.Parallel()
	stats := Aggregate([]Record{{
		RunType:            RunTypeRefactor,
		Success:            true,
		FeeEarned:          2.5,
		NetReduction:       1,
		NetSymbolReduction: 4,
		StructuralMetrics:  true,
	}})
	if stats.TotalFees != 2.5 || stats.AvgNetReduction != 4 || stats.BestNetReduction != 4 {
		t.Fatalf("structural payout stats = %+v", stats)
	}
}

func TestAggregateSourceMetrics(t *testing.T) {
	t.Parallel()
	stats := Aggregate([]Record{
		{Success: true, Outcome: "success", FeeEarned: 1.5, NetReduction: 10, Model: "one"},
		{Success: false, Outcome: "post_tests", FeeEarned: 9, NetReduction: 100, Model: "two"},
		{Success: true, Outcome: "success", FeeEarned: 2.5, NetReduction: 30, NetSymbolReduction: 2, StructuralMetrics: true, Model: "one"},
		{Success: false, Outcome: "static_checks", Model: "two"},
		{RunType: RunTypePrep, Success: true, Outcome: "coverage_increased", Model: "one", Prep: &PrepContext{CoverageIncreased: true}},
		{RunType: RunTypePrep, Success: false, Outcome: "failed", Model: "one", Prep: &PrepContext{}},
		{RunType: RunTypePrep, Success: true, Outcome: "already_sufficient", Model: "one", Prep: &PrepContext{AlreadySufficient: true}},
	})
	if stats.TotalRuns != 7 || stats.Successes != 4 || stats.TotalFees != 4 || stats.AvgNetReduction != 6 || stats.MedianNetReduction != 6 || stats.BestNetReduction != 10 {
		t.Fatalf("aggregate metrics = %+v", stats)
	}
	if stats.RecentRuns != 7 || stats.RecentSuccesses != 4 || stats.PrepRuns != 2 || stats.PrepCoverageIncreases != 1 || stats.PrepCoverageRate != 0.5 {
		t.Fatalf("recent/prep metrics = %+v", stats)
	}
	if stats.TopFailureOutcome != "failed" || stats.TopFailureCount != 1 || stats.TopFailureRate != 1.0/3.0 {
		t.Fatalf("failure metrics = %+v", stats)
	}
}

func TestAggregatePrepCoverageTarget(t *testing.T) {
	t.Parallel()
	records := make([]Record, 10)
	for i := range records {
		increased := i < 9
		records[i] = Record{
			RunType: RunTypePrep,
			Success: increased,
			Prep:    &PrepContext{CoverageIncreased: increased},
		}
	}
	stats := Aggregate(records)
	if stats.PrepCoverageTarget != PrepCoverageTarget || stats.PrepCoverageRate != PrepCoverageTarget || !stats.PrepCoverageTargetMet || !stats.RecentPrepTargetMet {
		t.Fatalf("coverage target metrics = %+v", stats)
	}
}

func TestRunStatsScopesAllReposAndCurrentStrategy(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	history := NewHistory(HistoryPath(stateDir))
	records := []Record{
		{Timestamp: time.Now(), RepoPath: "/one", StrategyID: "current", Success: true, NetReduction: 3},
		{Timestamp: time.Now(), RepoPath: "/one", StrategyID: "older", Success: true, NetReduction: 4},
		{Timestamp: time.Now(), RepoPath: "/two", StrategyID: "current", Success: true, NetReduction: 5},
	}
	for _, record := range records {
		if err := history.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	repoReport, err := RunStats(StatsOptions{RepoPath: "/one", StateDir: stateDir, StrategyID: "current", RecentWindow: DefaultRecentWindow})
	if err != nil {
		t.Fatal(err)
	}
	if repoReport.Records != 2 || repoReport.TotalRuns != 2 || repoReport.CurrentStrategy.TotalRuns != 1 || repoReport.CurrentStrategy.BestNetReduction != 3 {
		t.Fatalf("repo report = %+v", repoReport)
	}
	allReport, err := RunStats(StatsOptions{RepoPath: "/one", StateDir: stateDir, StrategyID: "current", AllRepos: true, RecentWindow: DefaultRecentWindow})
	if err != nil {
		t.Fatal(err)
	}
	if allReport.Records != 3 || allReport.TotalRuns != 3 || allReport.CurrentStrategy.TotalRuns != 2 || allReport.CurrentStrategy.BestNetReduction != 5 {
		t.Fatalf("all-repos report = %+v", allReport)
	}
}
