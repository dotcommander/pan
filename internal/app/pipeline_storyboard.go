package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/storyboard"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// StoryboardOptions controls optional storyboard enrichments.
type StoryboardOptions struct {
	MaxPhases, MaxStages int
	Review, Compare      string
	CommandHelp          string
	RefreshSpec          bool
	RefreshAfter         time.Duration
}

// StoryboardResult carries the storyboard and command-help provenance.
type StoryboardResult struct {
	Storyboard     storyboard.Storyboard
	Root           string
	RefreshPath    string
	HelpProvenance string
}

// PipelineStoryboard scans root and adds only explicitly requested live
// enrichments. Command execution occurs only with CommandHelp set to execute.
func (s Service) PipelineStoryboard(ctx context.Context, root string, opts StoryboardOptions) (StoryboardResult, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return StoryboardResult{}, fmt.Errorf("resolve root: %w", err)
	}
	result := StoryboardResult{Root: absRoot}
	if opts.RefreshSpec {
		refresh, refreshErr := storyboard.RefreshStaleSpec(ctx, absRoot, opts.MaxPhases, opts.MaxStages, opts.RefreshAfter)
		// Refresh is advisory; the current repository can still be scanned.
		if refreshErr == nil {
			result.RefreshPath = refresh.Path
		}
	}
	scanned, err := scan.Scan(ctx, absRoot, scan.Config{MaxPhases: opts.MaxPhases, MaxStages: opts.MaxStages})
	if err != nil {
		return result, err
	}
	sb := storyboard.Build(scanned, absRoot, "")
	commands, provenance, helpErr := storyboard.ResolveCommandHelp(ctx, opts.CommandHelp, absRoot)
	if helpErr != nil {
		return result, helpErr
	}
	sb.Commands, sb.CommandHelp = commands, provenance
	compare := opts.Compare
	if compare == "" {
		compare = result.RefreshPath
	}
	if compare != "" {
		path, resolveErr := spec.Resolve(compare)
		if resolveErr != nil {
			return result, resolveErr
		}
		storyboard.EnrichDiff(&sb, scanned, path)
	}
	if opts.Review != "" {
		path, resolveErr := spec.Resolve(opts.Review)
		if resolveErr != nil {
			return result, resolveErr
		}
		index := symbols.New(absRoot, symbols.Config{})
		if buildErr := index.Build(ctx); buildErr != nil {
			return result, fmt.Errorf("symbols build: %w", buildErr)
		}
		storyboard.EnrichReview(ctx, &sb, absRoot, index.Ranked(), path)
	}
	storyboard.EnrichCommandDrift(&sb)
	result.Storyboard, result.HelpProvenance = sb, provenance
	return result, nil
}
