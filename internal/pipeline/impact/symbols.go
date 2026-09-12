package impact

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

func goSymbolFiles(root, target string, ranked []symbols.RankedFile) []string {
	topLevel := make(map[string]bool)
	fields := make(map[string]bool)
	for _, rf := range ranked {
		addSymbolFileMatch(root, target, rf, topLevel, fields)
	}
	if len(topLevel) > 0 {
		return sortedKeys(topLevel)
	}
	return sortedKeys(fields)
}

func addSymbolFileMatch(root, target string, ranked symbols.RankedFile, topLevel, fields map[string]bool) {
	if ranked.FileSymbols == nil || (ranked.Language != "" && ranked.Language != languageGo) {
		return
	}
	path := ranked.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return
	}
	for _, declaration := range file.Decls {
		matchDeclaration(target, declaration, normalizePath(ranked.Path, root), topLevel, fields)
	}
}

func matchDeclaration(target string, declaration ast.Decl, path string, topLevel, fields map[string]bool) {
	if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == target {
		topLevel[path] = true
		return
	}
	declarationGroup, ok := declaration.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, declared := range declarationGroup.Specs {
		matchDeclarationSpec(target, declared, path, topLevel, fields)
	}
}

func matchDeclarationSpec(target string, declared ast.Spec, path string, topLevel, fields map[string]bool) {
	switch value := declared.(type) {
	case *ast.TypeSpec:
		if value.Name.Name == target {
			topLevel[path] = true
		}
		if structType, ok := value.Type.(*ast.StructType); ok && structHasField(structType, target) {
			fields[path] = true
		}
	case *ast.ValueSpec:
		for _, name := range value.Names {
			if name.Name == target {
				topLevel[path] = true
				return
			}
		}
	}
}

func structHasField(structure *ast.StructType, target string) bool {
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.Name == target {
				return true
			}
		}
	}
	return false
}

// goImportedSymbolCommands finds root-package functions that reference a
// declaration through its imported package, then projects those functions onto
// the dispatcher command closures. It deliberately requires both package alias
// and symbol-name agreement to avoid same-name matches from unrelated imports.
func goImportedSymbolCommands(root, target string, symbolFiles []string) []string {
	targetPackages := symbolPackages(root, symbolFiles)
	if len(targetPackages) == 0 {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	commands := make(map[string]bool)
	for _, entry := range entries {
		addImportedSymbolCommands(root, target, targetPackages, entry, commands)
	}
	return sortedKeys(commands)
}

func symbolPackages(root string, symbolFiles []string) map[string]bool {
	packages := make(map[string]bool)
	for _, filePath := range symbolFiles {
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(root, filePath)
		}
		file, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.PackageClauseOnly)
		if err == nil {
			packages[file.Name.Name] = true
		}
	}
	return packages
}

func addImportedSymbolCommands(root, target string, packages map[string]bool, entry os.DirEntry, commands map[string]bool) {
	if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
		return
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, entry.Name()), nil, parser.SkipObjectResolution)
	if err != nil || file.Name.Name != "main" {
		return
	}
	aliases := importedAliases(file, packages)
	if len(aliases) == 0 {
		return
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && functionReferencesTarget(function, target, aliases) {
			addReachedCommands(root, function.Name.Name, commands)
		}
	}
}

func importedAliases(file *ast.File, packages map[string]bool) map[string]bool {
	aliases := make(map[string]bool)
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || !packages[filepath.Base(path)] {
			continue
		}
		alias := filepath.Base(path)
		if imported.Name != nil {
			alias = imported.Name.Name
		}
		if alias != "." && alias != "_" {
			aliases[alias] = true
		}
	}
	return aliases
}

func functionReferencesTarget(function *ast.FuncDecl, target string, aliases map[string]bool) bool {
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != target {
			return true
		}
		owner, ok := selector.X.(*ast.Ident)
		if ok && aliases[owner.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

func addReachedCommands(root, function string, commands map[string]bool) {
	reached, ok := scan.CommandsReachingFunction(root, function)
	if !ok {
		return
	}
	for _, command := range reached {
		commands[command] = true
	}
}
