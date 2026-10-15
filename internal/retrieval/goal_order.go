package retrieval

import (
	"slices"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// OrderForGoal prioritizes the same goal evidence as task packets, retaining
// unmatched files in their original order. It does not mutate input or scores.
// Without positive evidence, structural orientation remains unchanged.
func OrderForGoal(snap analyze.Snapshot, ranked []ranking.RankedFile, goal string) []ranking.RankedFile {
	candidates := taskCandidates(ranked, snap, goal)
	if len(candidates) == 0 || candidates[0].fallback {
		return slices.Clone(ranked)
	}
	ordered := make([]ranking.RankedFile, 0, len(ranked))
	seen := make(map[string]bool, len(ranked))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate.file)
		seen[candidate.file.Path] = true
	}
	for _, file := range ranked {
		if !seen[file.Path] {
			ordered = append(ordered, file)
		}
	}
	return ordered
}
