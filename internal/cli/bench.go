package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/bench"
	"github.com/dotcommander/pan/internal/cache"
)

// BenchCmd groups deterministic benchmark harnesses.
type BenchCmd struct {
	Retrieval BenchRetrievalCmd `cmd:"" help:"Score retrieval against an issue-to-file dataset."`
}

// BenchRetrievalCmd is `pan bench retrieval`: it scores Pan's retrieval
// packet and a BM25 baseline on file-recall metrics against the gold patch
// paths of a local SWE-bench-style dataset. Everything is local: the dataset
// is a JSONL file, repositories come from git mirrors under --mirrors named
// org__name, and checkouts are read-only `git archive` extractions under the
// work directory.
type BenchRetrievalCmd struct {
	Dataset     string   `name:"dataset" type:"path" required:"" help:"SWE-bench-style JSONL dataset (instance_id, repo, base_commit, problem_statement, patch)."`
	Mirrors     string   `name:"mirrors" type:"path" required:"" help:"Directory holding local git mirrors named org__name."`
	Repo        []string `name:"repository" help:"Score only this repository (org/name); repeatable."`
	Limit       int      `name:"limit" help:"Score at most N instances; 0 scores all."`
	Work        string   `name:"work" type:"path" help:"Work directory for per-instance checkouts; defaults to pan-bench under the user cache directory."`
	TopRows     int      `name:"top-rows" default:"50" help:"Maximum per-instance detail rows; 0 lists all."`
	TokenBudget int      `name:"token-budget" default:"4096" help:"Per-case retrieval token budget."`
	Policy      string   `name:"policy" default:"structural-lexical/v1" enum:"structural-lexical/v1,structural-reference-graph/v1" help:"Retrieval policy under evaluation."`
}

// Run scores the dataset and emits the pan.retrieval-bench/v1 report.
func (c BenchRetrievalCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	switch {
	case c.Limit < 0:
		return errors.New("--limit must not be negative")
	case c.TopRows < 0:
		return errors.New("--top-rows must not be negative")
	case c.TokenBudget <= 0:
		return errors.New("--token-budget must be positive")
	}
	work := c.Work
	if work == "" {
		base, err := cache.DefaultDir()
		if err != nil {
			return err
		}
		work = filepath.Join(base, "bench")
	}
	absolute, err := filepath.Abs(work)
	if err != nil {
		return err
	}
	dataset, err := filepath.Abs(c.Dataset)
	if err != nil {
		return err
	}
	mirrors, err := filepath.Abs(c.Mirrors)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dataset); err != nil {
		return fmt.Errorf("dataset: %w", err)
	}
	report, err := deps.App.BenchRetrieval(ctx, bench.RunOptions{
		Dataset:     dataset,
		Mirrors:     mirrors,
		Work:        absolute,
		RepoFilters: c.Repo,
		Limit:       c.Limit,
		TopRows:     c.TopRows,
		TokenBudget: c.TokenBudget,
		Policy:      c.Policy,
	})
	if err != nil {
		return err
	}
	if report.Scored == 0 {
		// Keep every skip visible instead of reporting empty aggregates.
		for _, skip := range report.Skipped {
			fmt.Fprintf(deps.Err, "skipped %s: %s\n", skip.ID, skip.Reason)
		}
	}
	return emitResult(kctx, root, deps, mirrors, report)
}
