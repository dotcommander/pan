package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/retrieval"
)

// selectCallSymbol does not guess between identical declarations. An exact
// handle resolves one symbol; an unqualified name must identify only one.
func selectCallSymbol(ranked []ranking.RankedFile, selector string) (*retrieval.SymbolMatch, error) {
	matches := retrieval.Find(ranked, selector, "", "")
	if !strings.HasPrefix(selector, "symbol:") {
		matches = slices.DeleteFunc(matches, func(m retrieval.SymbolMatch) bool { return m.Basis != retrieval.BasisExact })
	}
	if len(matches) == 0 {
		return nil, nil // External or omitted declaration; retain lexical call evidence.
	}
	if len(matches) > 1 {
		handles := make([]string, 0, min(5, len(matches)))
		for _, match := range matches[:min(5, len(matches))] {
			handles = append(handles, match.Handle)
		}
		return nil, fmt.Errorf("ambiguous symbol %q; use a handle: %s", selector, strings.Join(handles, ", "))
	}
	return &matches[0], nil
}

func selectedCallEdges(snap analyze.Snapshot, selector string, match *retrieval.SymbolMatch) []analyze.Edge {
	var edges []analyze.Edge
	if match == nil {
		for _, edge := range snap.Edges {
			if edge.Kind == "calls" && edge.Target == nil && edge.Confidence == analyze.ConfidenceLexical && (edge.From == selector || edge.To == selector) {
				edges = append(edges, edge)
			}
		}
		return edges
	}
	uniqueName := true
	for _, symbol := range snap.Symbols {
		if symbol.Name == match.Symbol.Name && (symbol.Location != match.Symbol.Location || symbol.Kind != match.Symbol.Kind) {
			uniqueName = false
			break
		}
	}
	for _, edge := range snap.Edges {
		if edge.Kind != "calls" {
			continue
		}
		incoming := edge.CallsSymbol(match.Symbol) || (uniqueName && edge.Target == nil && edge.Confidence == analyze.ConfidenceLexical && edge.To == match.Symbol.Name)
		outgoing := edge.From == match.Symbol.Name && edge.Location.Path == match.File && edge.Location.Line >= match.Symbol.Location.Line &&
			(match.Symbol.EndLine < match.Symbol.Location.Line || edge.Location.Line <= match.Symbol.EndLine)
		if incoming || outgoing {
			edges = append(edges, edge)
		}
	}
	return edges
}
