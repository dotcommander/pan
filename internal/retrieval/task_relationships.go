package retrieval

import (
	"path"
	"slices"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// taskRelationships holds indexes for a single packet build. Ranked order is
// retained for importers and first-path ownership; caller output is sorted.
type taskRelationships struct {
	byPath      map[string]ranking.RankedFile
	importers   map[string][]string
	callsByName map[string][]analyze.Edge
	occurrences map[string]int
}

func newTaskRelationships(snap analyze.Snapshot, ranked []ranking.RankedFile) taskRelationships {
	index := taskRelationships{
		byPath:      make(map[string]ranking.RankedFile, len(ranked)),
		importers:   make(map[string][]string),
		callsByName: make(map[string][]analyze.Edge),
		occurrences: make(map[string]int),
	}
	for _, file := range ranked {
		if _, exists := index.byPath[file.Path]; !exists {
			index.byPath[file.Path] = file
		}
		seen := make(map[string]bool, len(file.InternalImports))
		for _, dir := range file.InternalImports {
			if !seen[dir] {
				index.importers[dir] = append(index.importers[dir], file.Path)
				seen[dir] = true
			}
		}
	}
	for _, symbol := range snap.Symbols {
		index.occurrences[symbol.Name]++
	}
	for _, edge := range snap.Edges {
		if edge.Kind == edgeKindCalls {
			index.callsByName[edge.To] = append(index.callsByName[edge.To], edge)
		}
	}
	return index
}

func (index taskRelationships) importersOf(file ranking.RankedFile) []string {
	var out []string
	for _, importer := range index.importers[path.Dir(file.Path)] {
		if importer != file.Path {
			out = append(out, importer)
		}
	}
	return out
}

// callersOf preserves lexical uniqueness and exact typed target matching.
func (index taskRelationships) callersOf(file ranking.RankedFile) []string {
	seen := make(map[string]bool)
	var out []string
	for _, symbol := range file.Symbols {
		for _, edge := range index.callsByName[symbol.Name] {
			if edge.Location.Path == file.Path {
				continue
			}
			if edge.Target != nil {
				if edge.Confidence != analyze.ConfidenceConfirmed || !edge.CallsSymbol(symbol) {
					continue
				}
			} else if edge.Confidence != analyze.ConfidenceLexical || index.occurrences[edge.To] > 1 {
				continue
			}
			if !seen[edge.Location.Path] {
				seen[edge.Location.Path] = true
				out = append(out, edge.Location.Path)
			}
		}
	}
	slices.Sort(out)
	return out
}
