package scan

import (
	"sort"

	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// importGraph maps relative file path → set of relative file paths it imports (internal only).
type importGraphT map[string][]string

// buildImportGraph constructs a file-level import graph from the ranked file list.
// It maps each file's relative path to the relative paths of internal files it imports.
// "Internal" means the import path has the module prefix.
func buildImportGraph(ranked []symbols.RankedFile, root string) importGraphT {
	// Build importPath → []relPath index (one package can span multiple files).
	importToFiles := make(map[string][]string, len(ranked))
	for _, rf := range ranked {
		if rf.FileSymbols == nil || rf.ImportPath == "" {
			continue
		}
		rel := relPath(root, rf.Path)
		importToFiles[rf.ImportPath] = append(importToFiles[rf.ImportPath], rel)
	}

	graph := make(importGraphT, len(ranked))
	for _, rf := range ranked {
		if rf.FileSymbols == nil {
			continue
		}
		rel := relPath(root, rf.Path)
		for _, imp := range rf.Imports {
			if deps, ok := importToFiles[imp]; ok {
				graph[rel] = append(graph[rel], deps...)
			}
		}
	}
	return graph
}

// transitiveImportClosure returns the set of all internal files reachable from
// startRel (the command entry file) via the import graph. The starting file
// is included in the closure.
func transitiveImportClosure(startRel string, graph importGraphT) map[string]bool {
	visited := make(map[string]bool)
	queue := []string{startRel}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, dep := range graph[cur] {
			if !visited[dep] {
				queue = append(queue, dep)
			}
		}
	}
	return visited
}

// sortedCommandNames returns command names sorted alphabetically for determinism.
func sortedCommandNames(commandFiles map[string]string) []string {
	names := make([]string, 0, len(commandFiles))
	for name := range commandFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
