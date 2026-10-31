package analyze

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// addSemanticGoCalls uses Go type information to resolve the target of each
// call. Syntax parsing still supplies symbols if a module cannot be loaded.
func (b *builder) addSemanticGoCalls() {
	patterns := semanticGoPatterns(b.snap.Files)
	if len(patterns) == 0 {
		return
	}
	loaded, err := packages.Load(&packages.Config{Context: b.ctx, Dir: b.snap.Root, Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedCompiledGoFiles | packages.NeedName}, patterns...)
	if err != nil {
		b.diag(Diagnostic{Level: diagnosticWarning, Message: "semantic Go analysis: " + err.Error()})
		return
	}
	targets := b.goCallTargets(loaded)
	resolved := make(map[Location]Edge)
	conflicted := make(map[Location]bool)
	for _, pkg := range loaded {
		if pkg.TypesInfo == nil || pkg.Fset == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			b.addPackageCalls(pkg, file, targets, resolved, conflicted)
		}
	}
	// Replace only the lexical evidence for the same expression. A different
	// call on the same line retains its own lexical fallback.
	remaining := b.snap.Edges[:0]
	for _, edge := range b.snap.Edges {
		if edge.Kind == "calls" && edge.Confidence == ConfidenceLexical {
			if typed, ok := resolved[edge.Location]; ok && typed.From == edge.From && typed.To == edge.To {
				b.nodes--
				continue
			}
		}
		remaining = append(remaining, edge)
	}
	b.snap.Edges = remaining
	sites := make([]Location, 0, len(resolved))
	for site := range resolved {
		sites = append(sites, site)
	}
	slices.SortFunc(sites, func(a, b Location) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Column - b.Column
	})
	for _, site := range sites {
		b.addEdge(resolved[site])
	}
}

// semanticGoPatterns selects only captured Go package directories. Imports are
// still loaded for type information, but are not explicit analysis targets.
func semanticGoPatterns(files []File) []string {
	directories := make(map[string]struct{})
	for _, file := range files {
		if file.Language != LanguageGo || !filepath.IsLocal(filepath.FromSlash(file.Path)) {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(file.Path)))
		// go list treats any "..." in a pattern as a wildcard. Leave such
		// directories to lexical evidence rather than widening the load.
		if strings.Contains(dir, "...") {
			continue
		}
		if dir == "." {
			directories["."] = struct{}{}
		} else {
			directories["./"+dir] = struct{}{}
		}
	}
	patterns := make([]string, 0, len(directories))
	for dir := range directories {
		patterns = append(patterns, dir)
	}
	slices.Sort(patterns)
	return patterns
}

func (b *builder) addPackageCalls(pkg *packages.Package, file *ast.File, targets map[types.Object]Location, resolved map[Location]Edge, conflicted map[Location]bool) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok {
				b.addTypedCall(pkg, call, function.Name.Name, targets, resolved, conflicted)
			}
			return true
		})
	}
}

func (b *builder) addTypedCall(pkg *packages.Package, call *ast.CallExpr, caller string, targets map[types.Object]Location, resolved map[Location]Edge, conflicted map[Location]bool) {
	callee := callObject(pkg.TypesInfo, call)
	if callee == nil {
		return
	}
	target, ok := targets[callee]
	if !ok {
		return // The declaration is outside the bounded snapshot: keep lexical evidence.
	}
	position := pkg.Fset.Position(call.Pos())
	rel := b.goPackagePath(position.Filename)
	if rel == "" {
		return
	}
	site := Location{Path: rel, Line: position.Line, Column: position.Column}
	if conflicted[site] {
		return
	}
	edge := Edge{From: caller, To: callee.Name(), Kind: "calls", Confidence: ConfidenceConfirmed, Location: site, Target: &target}
	if previous, exists := resolved[site]; exists && *previous.Target != target {
		delete(resolved, site)
		conflicted[site] = true
		return
	}
	resolved[site] = edge
}

func callObject(info *types.Info, call *ast.CallExpr) types.Object {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return info.ObjectOf(function)
	case *ast.SelectorExpr:
		if selection := info.Selections[function]; selection != nil {
			return selection.Obj()
		}
		return info.ObjectOf(function.Sel)
	default:
		return nil
	}
}
