package scan

import (
	"io"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// structuralRiskSignals carries graph and declaration evidence for one snapshot.
type structuralRiskSignals struct {
	importedBy map[string]int
	dependsOn  map[string]int
	symbols    map[string]int
	signatures map[string]int
}

// structuralSignals derives per-file dependency and symbol counts from
// snapshot edges and symbols.
func structuralSignals(snap analyze.Snapshot) structuralRiskSignals {
	importPath := moduleImportPath(snap.Root)
	result := structuralRiskSignals{
		importedBy: map[string]int{}, dependsOn: map[string]int{},
		symbols: map[string]int{}, signatures: map[string]int{},
	}
	importsPerFile := map[string]map[string]struct{}{}
	for _, edge := range snap.Edges {
		if edge.Kind != edgeKindImports {
			continue
		}
		if importsPerFile[edge.From] == nil {
			importsPerFile[edge.From] = map[string]struct{}{}
		}
		importsPerFile[edge.From][edge.To] = struct{}{}
	}
	for file, imports := range importsPerFile {
		result.dependsOn[file] = len(imports)
	}
	if importPath != "" {
		for _, edge := range snap.Edges {
			if edge.Kind != edgeKindImports {
				continue
			}
			if strings.HasPrefix(edge.To, importPath+"/") || edge.To == importPath {
				result.importedBy[edge.To]++
			}
		}
	}
	for _, symbol := range snap.Symbols {
		result.symbols[symbol.Location.Path]++
		if symbol.Exported && len(symbol.Signature) >= 80 {
			result.signatures[symbol.Location.Path]++
		}
	}
	return result
}

// packageImportPath reconstructs a file's Go import path from the module
// path declared in go.mod; empty when the module path is unknown.
func packageImportPath(root, rel string) string {
	module := moduleImportPath(root)
	if module == "" {
		return ""
	}
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return module
	}
	return module + "/" + dir
}

var modulePattern = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// moduleImportPath reads the module path from a bounded go.mod read.
func moduleImportPath(root string) string {
	file, err := os.Open(path.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxGoModBytes))
	if err != nil {
		return ""
	}
	match := modulePattern.FindSubmatch(data)
	if len(match) < 2 {
		return ""
	}
	return string(match[1])
}
