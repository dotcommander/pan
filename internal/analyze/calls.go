package analyze

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// CallSite is one parser-confirmed call expression in a supported non-Go
// source file. Text is the call expression's source span.
type CallSite struct {
	Line int
	Text string
}

// TreeSitterCalls extracts call-expression spans from a parsed source file.
// Go callers retain the Go AST path; the tree-sitter grammars cover the other
// languages carried by Snapshot.
func TreeSitterCalls(source []byte, language string) ([]CallSite, error) {
	if language == languageGo {
		return goCalls(source)
	}
	grammar := treeSitterLanguage(language)
	if grammar == nil {
		return nil, nil
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammar); err != nil {
		return nil, fmt.Errorf("set %s grammar: %w", language, err)
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse %s source", language)
	}
	defer tree.Close()
	var calls []CallSite
	var walk func(*tree_sitter.Node)
	walk = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		if isCallNode(node.Kind()) {
			calls = append(calls, CallSite{Line: int(node.StartPosition().Row) + 1, Text: node.Utf8Text(source)})
		}
		cursor := node.Walk()
		children := node.NamedChildren(cursor)
		cursor.Close()
		for index := range children {
			walk(&children[index])
		}
	}
	root := tree.RootNode()
	walk(root)
	return calls, nil
}

func goCalls(source []byte) ([]CallSite, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "source.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var calls []CallSite
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		start, end := fset.Position(call.Pos()).Offset, fset.Position(call.End()).Offset
		if start >= 0 && end >= start && end <= len(source) {
			calls = append(calls, CallSite{Line: fset.Position(call.Pos()).Line, Text: string(source[start:end])})
		}
		return true
	})
	return calls, nil
}

func isCallNode(kind string) bool {
	return strings.Contains(kind, "call") && !strings.Contains(kind, "declaration")
}
