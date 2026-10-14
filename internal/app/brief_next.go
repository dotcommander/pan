package app

import (
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/retrieval"
)

type briefCallTarget struct {
	path string
	name string
	line int
}

// briefCallCommands credits callers to their declaration, not the first symbol
// in a connected file. Index once so selection does not rescan the call graph
// for every ranked symbol. Unresolved names qualify only when unambiguous.
func briefCallCommands(snap analyze.Snapshot, ranked []ranking.RankedFile) []BriefNextCommand {
	declarations := make(map[briefCallTarget]struct{})
	names := make(map[string]int)
	lexicalTargets := make(map[string]briefCallTarget)
	for _, symbol := range snap.Symbols {
		names[symbol.Name]++
		if symbol.Kind != "function" && symbol.Kind != "method" {
			continue
		}
		target := briefCallTarget{symbol.Location.Path, symbol.Name, symbol.Location.Line}
		declarations[target] = struct{}{}
		lexicalTargets[symbol.Name] = target
	}
	callers := make(map[briefCallTarget]map[string]struct{})
	for _, edge := range snap.Edges {
		if edge.Kind != callsEdgeKind {
			continue
		}
		var target briefCallTarget
		if edge.Target != nil {
			target = briefCallTarget{edge.Target.Path, edge.To, edge.Target.Line}
		} else if edge.Confidence == analyze.ConfidenceLexical && names[edge.To] == 1 {
			target = lexicalTargets[edge.To]
		} else {
			continue
		}
		if _, ok := declarations[target]; !ok {
			continue
		}
		if edge.Location.Path == "" || edge.Location.Path == target.path {
			continue
		}
		if callers[target] == nil {
			callers[target] = make(map[string]struct{})
		}
		callers[target][edge.Location.Path] = struct{}{}
	}
	for _, file := range ranked {
		for _, symbol := range file.Symbols {
			if symbol.Kind != "function" && symbol.Kind != "method" {
				continue
			}
			target := briefCallTarget{file.Path, symbol.Name, symbol.Location.Line}
			if len(callers[target]) < 2 {
				continue
			}
			handle := retrieval.SymbolHandle(file.Path, symbol)
			if handle == "" {
				continue
			}
			return []BriefNextCommand{{
				Args:   []string{"flow", callsEdgeKind, handle, "--depth", "2"},
				Reason: "trace callers of a highly connected symbol",
			}}
		}
	}
	return nil
}
