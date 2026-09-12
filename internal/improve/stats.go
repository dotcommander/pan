package improve

import (
	"fmt"
	"math"
	"slices"
)

const (
	// DefaultRecentWindow is the trailing run count behind the "recent"
	// aggregate fields when no configured window applies.
	DefaultRecentWindow = 10
	plateauWindow       = 5
	plateauEpsilon      = 1.0
	// PrepCoverageTarget is the minimum fraction of attempted prep runs that
	// must increase coverage before the prep aggregate meets its target.
	PrepCoverageTarget = 0.90
)

// StatsSchema stamps every stats report.
const StatsSchema = "pan.improve-stats/v1"

// StatsOptions configures one read-only stats query. AllRepos lifts the
// default repository filter. A non-positive RecentWindow disables recent
// aggregate fields.
type StatsOptions struct {
	RepoPath     string
	StateDir     string
	StrategyID   string
	AllRepos     bool
	RecentWindow int
}

// StatsReport is the deterministic outcome summary for local run history.
// CurrentStrategy is scoped to StrategyID after the repository filter.
// Read-only: it never writes the ledger.
type StatsReport struct {
	Schema       string `json:"schema"`
	RepoPath     string `json:"repo_path"`
	HistoryPath  string `json:"history_path"`
	Records      int    `json:"records"`
	RecentWindow int    `json:"recent_window"`
	StrategyID   string `json:"strategy_id,omitempty"`
	AllRepos     bool   `json:"all_repos,omitempty"`
	Stats
	CurrentStrategy Stats `json:"current_strategy"`
}

// RunStats loads the local ledger, filters it to one repository, and
// aggregates it deterministically. A missing ledger reads as zero runs.
func RunStats(opts StatsOptions) (StatsReport, error) {
	history := NewHistory(HistoryPath(opts.StateDir))
	records, err := history.Load()
	if err != nil {
		return StatsReport{}, fmt.Errorf("load run history: %w", err)
	}
	relevant := records
	if !opts.AllRepos {
		relevant = RecordsForRepo(records, opts.RepoPath)
	}
	current := recordsForStrategy(relevant, opts.StrategyID)
	return StatsReport{
		Schema:          StatsSchema,
		RepoPath:        opts.RepoPath,
		HistoryPath:     history.Path(),
		Records:         len(relevant),
		RecentWindow:    opts.RecentWindow,
		StrategyID:      opts.StrategyID,
		AllRepos:        opts.AllRepos,
		Stats:           AggregateWindow(relevant, opts.RecentWindow),
		CurrentStrategy: AggregateWindow(current, opts.RecentWindow),
	}, nil
}

func recordsForStrategy(records []Record, strategyID string) []Record {
	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		if record.StrategyID == strategyID {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

// Stats summarizes local improve run-history records in one pass. All
// fields are derived deterministically from the ledger.
type Stats struct {
	TotalRuns              int            `json:"total_runs"`
	Successes              int            `json:"successes"`
	SuccessRate            float64        `json:"success_rate"`
	RefactorRuns           int            `json:"refactor_runs"`
	PrepRuns               int            `json:"prep_runs"`
	RecentRuns             int            `json:"recent_runs"`
	RecentSuccesses        int            `json:"recent_successes"`
	RecentSuccessRate      float64        `json:"recent_success_rate"`
	AvgNetReduction        float64        `json:"avg_net_reduction"`
	BestNetReduction       int            `json:"best_net_reduction"`
	MedianNetReduction     float64        `json:"median_net_reduction"`
	TotalSymbolsDeleted    int            `json:"total_symbols_deleted"`
	TotalFees              float64        `json:"total_fees"`
	PrepCoverageIncreases  int            `json:"prep_coverage_increases"`
	PrepCoverageRate       float64        `json:"prep_coverage_rate"`
	PrepCoverageTarget     float64        `json:"prep_coverage_target"`
	PrepCoverageTargetMet  bool           `json:"prep_coverage_target_met"`
	RecentPrepRuns         int            `json:"recent_prep_runs"`
	RecentPrepIncreases    int            `json:"recent_prep_increases"`
	RecentPrepCoverageRate float64        `json:"recent_prep_coverage_rate"`
	RecentPrepTargetMet    bool           `json:"recent_prep_target_met"`
	NetReductionConfidence *float64       `json:"net_reduction_confidence,omitempty"`
	Plateauing             bool           `json:"plateauing"`
	TrendSlope             float64        `json:"trend_slope"`
	TopFailureOutcome      string         `json:"top_failure_outcome,omitempty"`
	TopFailureCount        int            `json:"top_failure_count,omitempty"`
	TopFailureRate         float64        `json:"top_failure_rate,omitempty"`
	ByOutcome              map[string]int `json:"by_outcome"`
	ByRunType              map[string]int `json:"by_run_type"`
	ByModel                map[string]int `json:"by_model"`
	ByTargetRiskLane       map[string]int `json:"by_target_risk_lane"`
	ByPrepTargetOutcome    map[string]int `json:"by_prep_target_outcome"`
	ByCandidateLane        map[string]int `json:"by_candidate_lane"`
	ByActionability        map[string]int `json:"by_actionability"`
	BySkippedSignal        map[string]int `json:"by_skipped_signal"`
	DryRunOnly             bool           `json:"dry_run_only"`
}

// Aggregate summarizes records in a single deterministic pass.
func Aggregate(records []Record) Stats {
	return aggregateRecords(records, DefaultRecentWindow)
}

// AggregateWindow is Aggregate with an explicit recent window; a
// non-positive window disables the recent fields.
func AggregateWindow(records []Record, window int) Stats {
	if window < 0 {
		window = 0
	}
	return aggregateRecords(records, window)
}

// aggregateRecords is the single-pass core shared by Aggregate and
// AggregateWindow.
func aggregateRecords(records []Record, window int) Stats {
	stats := Stats{
		ByOutcome:           make(map[string]int),
		ByRunType:           make(map[string]int),
		ByModel:             make(map[string]int),
		ByTargetRiskLane:    make(map[string]int),
		ByPrepTargetOutcome: make(map[string]int),
		ByCandidateLane:     make(map[string]int),
		ByActionability:     make(map[string]int),
		BySkippedSignal:     make(map[string]int),
		PrepCoverageTarget:  PrepCoverageTarget,
		DryRunOnly:          true,
	}
	reductions := &reductionTally{failureOutcomes: make(map[string]int)}
	recentStart := len(records) - min(window, len(records))

	for i, rec := range records {
		stats.TotalRuns++
		accumulateRecord(&stats, rec, i >= recentStart, reductions)
	}
	successfulReductions := reductions.values

	if stats.TotalRuns > 0 {
		stats.SuccessRate = float64(stats.Successes) / float64(stats.TotalRuns)
	}
	if stats.RecentRuns > 0 {
		stats.RecentSuccessRate = float64(stats.RecentSuccesses) / float64(stats.RecentRuns)
	}
	if len(successfulReductions) > 0 {
		stats.AvgNetReduction = float64(reductions.totalNet) / float64(len(successfulReductions))
		stats.MedianNetReduction = median(successfulReductions)
		stats.NetReductionConfidence = netReductionConfidence(successfulReductions)
	}
	if stats.PrepRuns > 0 {
		stats.PrepCoverageRate = float64(stats.PrepCoverageIncreases) / float64(stats.PrepRuns)
		stats.PrepCoverageTargetMet = stats.PrepCoverageRate >= PrepCoverageTarget
	}
	if stats.RecentPrepRuns > 0 {
		stats.RecentPrepCoverageRate = float64(stats.RecentPrepIncreases) / float64(stats.RecentPrepRuns)
		stats.RecentPrepTargetMet = stats.RecentPrepCoverageRate >= PrepCoverageTarget
	}
	stats.Plateauing, stats.TrendSlope = plateauSignal(successfulReductions, plateauWindow, plateauEpsilon)
	stats.TopFailureOutcome, stats.TopFailureCount, stats.TopFailureRate = topFailure(reductions.failureOutcomes, stats.TotalRuns-stats.Successes)
	return stats
}

// reductionTally accumulates the per-record signals aggregate needs after
// its single pass: successful non-prep reductions and failure outcomes.
type reductionTally struct {
	totalNet        int
	values          []float64
	failureOutcomes map[string]int
}

// accumulateRecord folds one record into stats and the tally; recent
// marks the record as inside the recent window.
func accumulateRecord(stats *Stats, rec Record, recent bool, reductions *reductionTally) {
	stats.ByModel[rec.Model]++
	stats.ByRunType[rec.RunType]++
	if rec.Outcome != "" {
		stats.ByOutcome[rec.Outcome]++
	}
	if !rec.DryRun {
		stats.DryRunOnly = false
	}
	if rec.RunType == RunTypeRefactor {
		stats.RefactorRuns++
	}
	if recent {
		addRecentRecord(stats, rec)
	}
	if prepCoverageAttempt(rec) {
		stats.PrepRuns++
		stats.PrepCoverageIncreases += boolInt(prepCoverageIncreased(rec))
	}
	addRecordLanes(stats, rec)
	if !rec.Success {
		if rec.Outcome != "" {
			reductions.failureOutcomes[rec.Outcome]++
		}
		return
	}
	stats.Successes++
	if rec.RunType == RunTypePrep || rec.Outcome == string(OutcomeNoCandidate) {
		return
	}
	netReduction := payoutNetReduction(rec)
	stats.TotalFees += rec.FeeEarned
	reductions.totalNet += netReduction
	reductions.values = append(reductions.values, float64(netReduction))
	if len(reductions.values) == 1 || netReduction > stats.BestNetReduction {
		stats.BestNetReduction = netReduction
	}
	if rec.RunType == RunTypeRefactor {
		stats.TotalSymbolsDeleted += rec.SymbolsDeleted
	}
}

func addRecentRecord(stats *Stats, rec Record) {
	stats.RecentRuns++
	if rec.Success {
		stats.RecentSuccesses++
	}
	if prepCoverageAttempt(rec) {
		stats.RecentPrepRuns++
		stats.RecentPrepIncreases += boolInt(prepCoverageIncreased(rec))
	}
}

func prepCoverageAttempt(rec Record) bool {
	return rec.RunType == RunTypePrep && rec.Prep != nil && !rec.Prep.AlreadySufficient
}
func prepCoverageIncreased(rec Record) bool {
	return prepCoverageAttempt(rec) && rec.Success && rec.Prep.CoverageIncreased
}

func payoutNetReduction(rec Record) int {
	if rec.StructuralMetrics {
		return rec.NetSymbolReduction
	}
	return rec.NetReduction
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func addRecordLanes(stats *Stats, rec Record) {
	if rec.Target != nil {
		for _, lane := range rec.Target.RiskLanes {
			stats.ByTargetRiskLane[lane]++
		}
	}
	if rec.Prep != nil {
		for _, target := range rec.Prep.Targets {
			if target.Outcome != "" {
				stats.ByPrepTargetOutcome[target.Outcome]++
			}
		}
	}
	if rec.Candidates == nil {
		return
	}
	for _, file := range rec.Candidates.Files {
		if file.Lane != "" {
			stats.ByCandidateLane[file.Lane]++
		}
		if file.Actionability != "" {
			stats.ByActionability[file.Actionability]++
		}
	}
	for _, signal := range rec.Candidates.SkippedSignals {
		if signal != "" {
			stats.BySkippedSignal[signal]++
		}
	}
}

func netReductionConfidence(values []float64) *float64 {
	if len(values) < 3 {
		return nil
	}
	baseline, best := values[0], slices.Max(values)
	if best == baseline {
		return nil
	}
	deviations := make([]float64, len(values))
	middle := median(values)
	for index, value := range values {
		deviations[index] = math.Abs(value - middle)
	}
	mad := median(deviations)
	if mad == 0 {
		return nil
	}
	confidence := math.Abs(best-baseline) / mad
	return &confidence
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// plateauSignal reports whether the last windowSize successful reductions
// are flat (least-squares slope magnitude under epsilon) and that slope.
func plateauSignal(values []float64, windowSize int, epsilon float64) (bool, float64) {
	if windowSize <= 0 || len(values) < windowSize {
		return false, 0
	}
	window := values[len(values)-windowSize:]
	slope := leastSquaresSlope(window)
	return math.Abs(slope) < epsilon, slope
}

func leastSquaresSlope(values []float64) float64 {
	n := float64(len(values))
	if n < 2 {
		return 0
	}
	var sumX, sumY, sumXY, sumXX float64
	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		return 0
	}
	return (n*sumXY - sumX*sumY) / denom
}

// topFailure returns the most frequent failure outcome, its count, and its
// share of all failures, breaking ties alphabetically for determinism.
func topFailure(outcomes map[string]int, totalFailures int) (string, int, float64) {
	if totalFailures == 0 {
		return "", 0, 0
	}
	var top string
	var count int
	for outcome, n := range outcomes {
		if n > count || n == count && outcome < top {
			top = outcome
			count = n
		}
	}
	if count == 0 {
		return "", 0, 0
	}
	return top, count, float64(count) / float64(totalFailures)
}
