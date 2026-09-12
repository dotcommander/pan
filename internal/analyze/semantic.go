package analyze

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// addSemanticGoCalls uses Go type information to resolve the target of each
// call. Syntax parsing still supplies symbols if a module cannot be loaded.
func (b *builder) addSemanticGoCalls() {
	if !hasGoFiles(b.snap.Files) {
		return
	}
	loaded, err := packages.Load(&packages.Config{Context: b.ctx, Dir: b.snap.Root, Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedCompiledGoFiles | packages.NeedName}, "./...")
	if err != nil {
		b.diag(Diagnostic{Level: diagnosticWarning, Message: "semantic Go analysis: " + err.Error()})
		return
	}
	for _, pkg := range loaded {
		if pkg.TypesInfo == nil || pkg.Fset == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			b.addPackageCalls(pkg, file)
		}
	}
}

func hasGoFiles(files []File) bool {
	for _, file := range files {
		if file.Language == languageGo {
			return true
		}
	}
	return false
}

func (b *builder) addPackageCalls(pkg *packages.Package, file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok {
				b.addTypedCall(pkg, call, function.Name.Name)
			}
			return true
		})
	}
}

func (b *builder) addTypedCall(pkg *packages.Package, call *ast.CallExpr, caller string) {
	callee := callObject(pkg.TypesInfo, call)
	if callee == nil || callee.Pkg() == nil {
		return
	}
	position := pkg.Fset.Position(call.Pos())
	rel, err := filepath.Rel(b.snap.Root, position.Filename)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	b.addEdge(Edge{From: caller, To: callee.Name(), Kind: "calls", Confidence: ConfidenceConfirmed, Location: Location{Path: filepath.ToSlash(rel), Line: position.Line}})
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
