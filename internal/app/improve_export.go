package app

import (
	"context"
	"time"

	"github.com/dotcommander/pan/internal/improve"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

func (s Service) ImproveExport(_ context.Context, output string, limit int, since *time.Time, cfg improveconfig.Config) (int, error) {
	_, stateDir, err := s.improvePolicy()
	if err != nil {
		return 0, err
	}
	if cfg.StateDir != "" {
		stateDir = cfg.StateDir
	}
	observations, err := improve.ExportObservations(improve.HistoryPath(stateDir), limit, since)
	if err != nil {
		return 0, err
	}
	if err := improve.WriteObservations(output, observations); err != nil {
		return 0, err
	}
	return len(observations), nil
}
