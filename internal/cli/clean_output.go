package cli

import (
	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/clean"
	"github.com/dotcommander/pan/internal/render"
)

// cleanResult carries walk coverage through cleanup result projections.
type cleanResult interface {
	CleanCoverage() clean.Coverage
}

func emitCleanResult(kctx *kong.Context, root *Root, deps Deps, repository string, result cleanResult) error {
	coverage := result.CleanCoverage()
	status := analyze.Status{
		Complete:     coverage.Complete,
		Limits:       coverage.Limits,
		Skipped:      coverage.Skipped,
		SkippedCount: coverage.SkippedCount,
	}
	envelope := render.NewEnvelope(commandPath(kctx), repository, status, result, nil)
	return render.Write(deps.Out, root.Format, envelope)
}
