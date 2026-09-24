package analyze

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// sourceBinding names only an explicit named import. It is not a proof of a
// target until resolution finds exactly one captured file and declaration.
type sourceBinding struct {
	Local  string
	Symbol string
	Module string
}

func namedBindings(root *tree_sitter.Node, source []byte, language string) ([]sourceBinding, map[string]bool) {
	var bindings []sourceBinding
	shadows := make(map[string]bool)
	var walk func(*tree_sitter.Node)
	walk = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		switch {
		case language == LanguagePython && node.Kind() == "import_from_statement":
			bindings = append(bindings, pythonFromBindings(node, source)...)
		case (language == LanguageTypescript || language == LanguageTsx || language == LanguageJavascript || language == LanguageJsx) && node.Kind() == "import_statement":
			bindings = append(bindings, typescriptNamedBindings(node, source)...)
		}
		markShadowBindings(node, source, shadows)
		cursor := node.Walk()
		children := node.NamedChildren(cursor)
		cursor.Close()
		for i := range children {
			walk(&children[i])
		}
	}
	walk(root)
	return bindings, shadows
}

func pythonFromBindings(node *tree_sitter.Node, source []byte) []sourceBinding {
	module := node.ChildByFieldName("module_name")
	if module == nil {
		return nil
	}
	var result []sourceBinding
	cursor := node.Walk()
	children := node.NamedChildren(cursor)
	cursor.Close()
	for i := range children {
		child := &children[i]
		if child.Kind() == "aliased_import" {
			name, alias := child.ChildByFieldName("name"), child.ChildByFieldName("alias")
			if name != nil && alias != nil && name.Kind() == "dotted_name" && !strings.Contains(name.Utf8Text(source), ".") {
				result = append(result, sourceBinding{Local: alias.Utf8Text(source), Symbol: name.Utf8Text(source), Module: module.Utf8Text(source)})
			}
		} else if child.Kind() == "dotted_name" && child.StartByte() != module.StartByte() && !strings.Contains(child.Utf8Text(source), ".") {
			name := child.Utf8Text(source)
			result = append(result, sourceBinding{Local: name, Symbol: name, Module: module.Utf8Text(source)})
		}
	}
	return result
}

func typescriptNamedBindings(node *tree_sitter.Node, source []byte) []sourceBinding {
	module := node.ChildByFieldName("source")
	if module == nil {
		return nil
	}
	path := strings.Trim(module.Utf8Text(source), "\"'` ")
	var result []sourceBinding
	cursor := node.Walk()
	clauses := node.NamedChildren(cursor)
	cursor.Close()
	for i := range clauses {
		if clauses[i].Kind() != "import_clause" {
			continue
		}
		clauseCursor := clauses[i].Walk()
		children := clauses[i].NamedChildren(clauseCursor)
		clauseCursor.Close()
		for j := range children {
			if children[j].Kind() != "named_imports" {
				continue
			}
			importsCursor := children[j].Walk()
			imports := children[j].NamedChildren(importsCursor)
			importsCursor.Close()
			for k := range imports {
				name, alias := imports[k].ChildByFieldName("name"), imports[k].ChildByFieldName("alias")
				if name == nil || name.Kind() != "identifier" {
					continue
				}
				local := name.Utf8Text(source)
				if alias != nil {
					local = alias.Utf8Text(source)
				}
				result = append(result, sourceBinding{Local: local, Symbol: name.Utf8Text(source), Module: path})
			}
		}
	}
	return result
}

// A binding anywhere in a file makes its imported homonym unsafe to attribute
// without full lexical-scope analysis. This conservative check can lose recall.
func markShadowBindings(node *tree_sitter.Node, source []byte, shadows map[string]bool) {
	var fields []string
	switch node.Kind() {
	case "assignment", "augmented_assignment", "for_statement":
		fields = []string{"left"}
	case "variable_declarator", "default_parameter", "function_definition", "class_definition":
		fields = []string{"name"}
	case "required_parameter", "optional_parameter":
		fields = []string{"name", "pattern"}
	case "parameters", "typed_parameter":
		cursor := node.Walk()
		children := node.NamedChildren(cursor)
		cursor.Close()
		for i := range children {
			if children[i].Kind() == "identifier" {
				markBoundNames(&children[i], source, shadows)
			}
		}
	}
	for _, field := range fields {
		markBoundNames(node.ChildByFieldName(field), source, shadows)
	}
}

func markBoundNames(node *tree_sitter.Node, source []byte, shadows map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind() == "identifier" || node.Kind() == "type_identifier" {
		shadows[node.Utf8Text(source)] = true
		return
	}
	cursor := node.Walk()
	children := node.NamedChildren(cursor)
	cursor.Close()
	for i := range children {
		markBoundNames(&children[i], source, shadows)
	}
}
