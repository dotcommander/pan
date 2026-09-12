package improve

import (
	"fmt"
	"strings"
)

// RecommendSchema stamps every recommendation report.
const RecommendSchema = "pan.improve-recommend/v1"

// Recommendation tasks select a guarded dry-run, never execute it.
const (
	TaskRefactor = "refactor"
	TaskPrep     = "prep"
	TaskDeadcode = "deadcode"
)

// RecommendOptions configures a read-only, repository-and-strategy-scoped query.
type RecommendOptions struct {
	RepoPath     string
	StateDir     string
	StrategyID   string
	ConfigPath   string
	Floor        float64
	MaxTargets   int
	RecentWindow int
	Exclude      []string
}

// RecommendReport combines the next task with supplemental local evidence.
type RecommendReport struct {
	Schema      string       `json:"schema"`
	RepoPath    string       `json:"repo_path"`
	StrategyID  string       `json:"strategy_id,omitempty"`
	Task        string       `json:"task"`
	Command     string       `json:"command"`
	Reason      string       `json:"reason"`
	DryRunOnly  bool         `json:"dry_run_only"`
	DeadSymbols int          `json:"dead_symbols"`
	Candidates  []DeadSymbol `json:"candidates,omitempty"`
	History     Stats        `json:"history"`
}

// RunRecommend banks coverage before choosing deterministic deletion or a
// provider proposal. Only runs of the current strategy affect the decision.
func RunRecommend(opts RecommendOptions) (RecommendReport, error) {
	records, err := NewHistory(HistoryPath(opts.StateDir)).Load()
	if err != nil {
		return RecommendReport{}, fmt.Errorf("load run history: %w", err)
	}
	relevant := RecordsForRepoAndStrategy(records, opts.RepoPath, opts.StrategyID)
	task, reason := recommendTask(relevant, opts.RecentWindow)
	report := RecommendReport{
		Schema: RecommendSchema, RepoPath: opts.RepoPath, StrategyID: opts.StrategyID,
		Task: task, Reason: reason, DryRunOnly: true,
		History: AggregateWindow(relevant, opts.RecentWindow),
		Command: recommendationCommand(opts, task),
	}
	// Deletion candidates supplement the selected lane; their presence cannot
	// override missing characterization coverage or a history gate.
	if task == TaskDeadcode {
		dead, detectErr := DetectDeadSymbols(opts.RepoPath, opts.Exclude)
		if detectErr != nil {
			return RecommendReport{}, fmt.Errorf("detect dead symbols: %w", detectErr)
		}
		report.DeadSymbols = len(dead)
		if opts.MaxTargets > 0 && len(dead) > opts.MaxTargets {
			dead = dead[:opts.MaxTargets]
		}
		report.Candidates = dead
	}
	return report, nil
}

// RecordsForRepoAndStrategy excludes results produced under different rules.
func RecordsForRepoAndStrategy(records []Record, repoPath, strategyID string) []Record {
	relevant := RecordsForRepo(records, repoPath)
	filtered := make([]Record, 0, len(relevant))
	for _, record := range relevant {
		if record.StrategyID == strategyID {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func recommendTask(records []Record, window int) (task, reason string) {
	if len(records) == 0 {
		return TaskPrep, "no run history for this repo and strategy; start by banking characterization coverage"
	}
	prepRecords, refactorRecords, sufficient := splitRecommendationRecords(records)
	prepStats := AggregateWindow(prepRecords, window)
	if prepStats.PrepRuns == 0 && !sufficient {
		return TaskPrep, "no coverage-lift prep attempt recorded for this repo"
	}
	if prepStats.RecentPrepRuns > 0 && !prepStats.RecentPrepTargetMet {
		return TaskPrep, fmt.Sprintf("recent prep coverage-lift rate is %.1f%%; keep preparing before refactor attempts", prepStats.RecentPrepCoverageRate*100)
	}
	stats := AggregateWindow(refactorRecords, window)
	if stats.TotalRuns == 0 {
		return TaskDeadcode, "prep is covered; try the deterministic no-LLM deletion lane first"
	}
	if stats.Plateauing {
		return TaskDeadcode, "recent refactor reductions are plateauing; prefer deterministic deletion"
	}
	switch stats.TopFailureOutcome {
	case "post_tests", "static_checks":
		return TaskDeadcode, "recent refactor failures are gate failures; choose the narrow deterministic lane"
	}
	if stats.RecentRuns > 0 && stats.RecentSuccessRate >= 0.5 {
		return TaskRefactor, fmt.Sprintf("recent refactor success rate is %.1f%%; run one guarded dry-run proposal", stats.RecentSuccessRate*100)
	}
	return TaskDeadcode, fmt.Sprintf("recent refactor success rate is %.1f%%; use deterministic dry-run work", stats.RecentSuccessRate*100)
}

func splitRecommendationRecords(records []Record) (prepRecords, refactorRecords []Record, sufficient bool) {
	for _, record := range records {
		if record.RunType != RunTypePrep {
			refactorRecords = append(refactorRecords, record)
			continue
		}
		prepRecords = append(prepRecords, record)
		sufficient = sufficient || record.Outcome == string(OutcomeAlreadySufficient) || record.AlreadySufficient || record.Prep != nil && record.Prep.AlreadySufficient
	}
	return prepRecords, refactorRecords, sufficient
}

func recommendationCommand(opts RecommendOptions, task string) string {
	command := []string{"pan", "--repo", opts.RepoPath, "improve"}
	if task == TaskPrep {
		command = append(command, TaskPrep)
	} else {
		command = append(command, TaskRefactor)
	}
	if task == TaskDeadcode {
		command = append(command, "--deadcode")
	}
	command = append(command, "--dry-run")
	if opts.ConfigPath != "" {
		command = append(command, "--config", opts.ConfigPath)
	}
	for index, arg := range command {
		command[index] = quoteRecommendationArg(arg)
	}
	return strings.Join(command, " ")
}

func quoteRecommendationArg(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool { return !isRecommendationSafeRune(r) }) < 0 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func isRecommendationSafeRune(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@", r)
}
