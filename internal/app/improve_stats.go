package app

import (
	"context"

	"github.com/dotcommander/pan/internal/improve"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

// ImproveStats summarizes the local run ledger for one repository.
func (s Service) ImproveStats(ctx context.Context, root string) (improve.StatsReport, error) {
	return s.ImproveStatsWithConfig(ctx, root, 0, false, improveconfig.Default())
}

// ImproveStatsWithRecentWindow overrides the configured history window when positive.
func (s Service) ImproveStatsWithRecentWindow(ctx context.Context, root string, recent int) (improve.StatsReport, error) {
	return s.ImproveStatsWithConfig(ctx, root, recent, false, improveconfig.Default())
}

// ImproveStatsWithConfig scopes history to the selected strategy by default.
// AllRepos keeps the source command's cross-repository summary available.
func (s Service) ImproveStatsWithConfig(ctx context.Context, root string, recent int, allRepos bool, improveCfg improveconfig.Config) (improve.StatsReport, error) {
	rules, stateDir, err := s.improvePolicy()
	if err != nil {
		return improve.StatsReport{}, err
	}
	if improveCfg.StateDir != "" {
		stateDir = improveCfg.StateDir
	}
	if recent > 0 {
		rules.RecentWindow = recent
	}
	return improve.RunStats(improve.StatsOptions{
		RepoPath: root, StateDir: stateDir, StrategyID: selectedStrategy(improveCfg),
		AllRepos: allRepos, RecentWindow: rules.RecentWindow,
	})
}
