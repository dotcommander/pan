package improve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

func goFunctionNames(target, root string, limit int) []string {
	if root == "" || limit <= 0 {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), target, data, 0)
	if err != nil {
		return nil
	}
	var names []string
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		names = append(names, fn.Name.Name)
		if len(names) == limit {
			break
		}
	}
	return names
}
