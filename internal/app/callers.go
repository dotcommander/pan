package app

import (
	"context"
	"errors"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/repo"
)

// Callers returns direct incoming call evidence, preserving exact declaration
// identity and lexical confidence. Depth is reserved, as it is for Calls.
func (s Service) Callers(ctx context.Context, root, selector string, depth int) (analyze.Snapshot, []analyze.Edge, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	if selector == "" {
		return snap, nil, errors.New("symbol selector is required")
	}
	if depth < 1 {
		return snap, nil, errors.New("depth must be positive")
	}
	match, err := selectCallSymbol(ranking.Rank(snap, repo.ModulePath(snap.Root), ranking.Options{}), selector)
	if err != nil {
		return snap, nil, err
	}
	name := selector
	if match != nil {
		name = match.Symbol.Name
	}
	result := make([]analyze.Edge, 0)
	for _, edge := range selectedCallEdges(snap, selector, match) {
		if (match != nil && edge.CallsSymbol(match.Symbol)) || (edge.Target == nil && edge.To == name) {
			result = append(result, edge)
		}
	}
	if len(result) == 0 {
		snap.Diagnostics = append(snap.Diagnostics, analyze.Diagnostic{Level: levelInfo, Message: "no matching callers found in analyzed graph"})
	}
	return snap, result, nil
}
