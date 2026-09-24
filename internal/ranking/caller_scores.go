package ranking

import "github.com/dotcommander/pan/internal/analyze"

type callDeclaration struct {
	file   string
	symbol analyze.Symbol
}

// applyCallerScores credits the actual in-snapshot target of resolved calls.
// Lexical fallback may credit a file only when the name has one declaration.
func applyCallerScores(ranked []RankedFile, snap analyze.Snapshot) {
	byName := make(map[string][]callDeclaration)
	byPath := make(map[string]*RankedFile, len(ranked))
	for i := range ranked {
		file := &ranked[i]
		byPath[file.Path] = file
		for _, symbol := range file.Symbols {
			byName[symbol.Name] = append(byName[symbol.Name], callDeclaration{file.Path, symbol})
		}
	}
	callers := make(map[string]map[string]struct{})
	lexical := make(map[string]bool)
	for _, edge := range snap.Edges {
		if edge.Kind != edgeCalls {
			continue
		}
		declarations := byName[edge.To]
		for _, declaration := range declarations {
			if declaration.file == edge.Location.Path {
				continue
			}
			if edge.Target != nil {
				if edge.Confidence != analyze.ConfidenceConfirmed || !edge.CallsSymbol(declaration.symbol) {
					continue
				}
			} else if edge.Confidence != analyze.ConfidenceLexical || len(declarations) != 1 {
				continue
			}
			if callers[declaration.file] == nil {
				callers[declaration.file] = make(map[string]struct{})
			}
			callers[declaration.file][edge.Location.Path] = struct{}{}
			if edge.Target == nil {
				lexical[declaration.file] = true
			}
		}
	}
	for file, sources := range callers {
		rankedFile := byPath[file]
		addComponent(rankedFile, ComponentCallers, min(20, len(sources)*2))
		rankedFile.CallerCount = len(sources)
		if lexical[file] {
			rankedFile.Confidence = analyze.ConfidenceLexical
		} else {
			rankedFile.Confidence = analyze.ConfidenceConfirmed
		}
	}
}
