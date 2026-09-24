package analyze

import (
	"go/ast"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

type goFuncKey struct {
	path string
	name string
	kind string
	line int
}

// goCallTargets indexes only declarations captured in the bounded snapshot.
// An imported object with no captured declaration remains lexical evidence.
func (b *builder) goCallTargets(loaded []*packages.Package) map[types.Object]Location {
	available := make(map[goFuncKey]int)
	for _, symbol := range b.snap.Symbols {
		if symbol.Kind != "function" && symbol.Kind != "method" {
			continue
		}
		key := goFuncKey{symbol.Location.Path, symbol.Name, symbol.Kind, symbol.Location.Line}
		available[key]++
	}
	targets := make(map[types.Object]Location)
	for _, pkg := range loaded {
		if pkg.TypesInfo == nil || pkg.Fset == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			path := b.goPackagePath(pkg.Fset.Position(file.Pos()).Filename)
			if path == "" {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				kind := "function"
				if fn.Recv != nil {
					kind = "method"
				}
				line := pkg.Fset.Position(fn.Pos()).Line
				if available[goFuncKey{path, fn.Name.Name, kind, line}] != 1 {
					continue
				}
				if object := pkg.TypesInfo.Defs[fn.Name]; object != nil {
					targets[object] = Location{Path: path, Line: line}
				}
			}
		}
	}
	return targets
}

func (b *builder) goPackagePath(filename string) string {
	if filename == "" {
		return ""
	}
	rel, err := filepath.Rel(b.snap.Root, filename)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	path := filepath.ToSlash(rel)
	if _, captured := b.snap.Captured[path]; !captured {
		return ""
	}
	return path
}
