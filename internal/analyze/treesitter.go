package analyze

import (
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tree_sitter_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_php "github.com/tree-sitter/tree-sitter-php/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type parsedSource struct {
	Symbols    []Symbol
	Imports    []string
	References []sourceReference
}

type sourceReference struct {
	Name string
	Line int
}

func parseTreeSitterBytes(source []byte, relative, language string) (parsedSource, error) {
	grammar := treeSitterLanguage(language)
	if grammar == nil {
		return parsedSource{}, nil
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammar); err != nil {
		return parsedSource{}, err
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return parsedSource{}, fmt.Errorf("parse %s source", language)
	}
	defer tree.Close()
	parsed := extractTreeSitter(tree.RootNode(), source, relative)
	references, queryErr := queryReferences(tree.RootNode(), source, language)
	if queryErr != nil {
		return parsedSource{}, queryErr
	}
	parsed.References = references
	return parsed, nil
}

func treeSitterLanguage(language string) *tree_sitter.Language {
	switch language {
	case "c":
		return tree_sitter.NewLanguage(tree_sitter_c.Language())
	case "cpp":
		return tree_sitter.NewLanguage(tree_sitter_cpp.Language())
	case "java":
		return tree_sitter.NewLanguage(tree_sitter_java.Language())
	case "php":
		return tree_sitter.NewLanguage(tree_sitter_php.LanguagePHP())
	case "python":
		return tree_sitter.NewLanguage(tree_sitter_python.Language())
	case "ruby":
		return tree_sitter.NewLanguage(tree_sitter_ruby.Language())
	case "rust":
		return tree_sitter.NewLanguage(tree_sitter_rust.Language())
	case "typescript", "javascript":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	case "tsx", "jsx":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX())
	default:
		return nil
	}
}

func extractTreeSitter(root *tree_sitter.Node, source []byte, relative string) parsedSource {
	extractor := treeSitterExtractor{source: source, relative: relative, symbols: map[string]struct{}{}, imports: map[string]struct{}{}}
	var walk func(*tree_sitter.Node)
	walk = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		extractor.add(node)
		cursor := node.Walk()
		children := node.NamedChildren(cursor)
		cursor.Close()
		for _, child := range children {
			walk(&child)
		}
	}
	walk(root)
	return extractor.parsedSource
}

type treeSitterExtractor struct {
	parsedSource
	source           []byte
	relative         string
	symbols, imports map[string]struct{}
}

func (e *treeSitterExtractor) add(node *tree_sitter.Node) {
	e.addSymbol(node)
	e.addImport(node)
}

func (e *treeSitterExtractor) addSymbol(node *tree_sitter.Node) {
	kind, name := treeSitterSymbolKind(node.Kind()), treeSitterName(node, e.source)
	if kind == "" || name == "" {
		return
	}
	position, end := node.StartPosition(), node.EndPosition()
	key := fmt.Sprintf("%s:%d:%s", kind, position.Row, name)
	if _, exists := e.symbols[key]; exists {
		return
	}
	e.symbols[key] = struct{}{}
	e.Symbols = append(e.Symbols, Symbol{Name: name, Kind: kind, Exported: true, EndLine: int(end.Row) + 1, Location: Location{Path: e.relative, Line: int(position.Row) + 1}})
}

func (e *treeSitterExtractor) addImport(node *tree_sitter.Node) {
	if !isTreeSitterImport(node.Kind()) {
		return
	}
	imported := treeSitterImport(node, e.source)
	if imported == "" {
		return
	}
	if _, exists := e.imports[imported]; exists {
		return
	}
	e.imports[imported] = struct{}{}
	e.Imports = append(e.Imports, imported)
}

func treeSitterSymbolKind(kind string) string {
	switch kind {
	case "function_definition", "function_declaration", "function_item", "method", "method_declaration", "method_definition", "constructor_declaration":
		return "function"
	case "class_declaration", "class_definition", "record_declaration":
		return "class"
	case "interface_declaration", "interface_definition":
		return "interface"
	case "struct_specifier", "struct_item":
		return "struct"
	case "enum_specifier", "enum_declaration", "enum_item":
		return "enum"
	case "trait_item":
		return "trait"
	case "type_definition", "type_alias_declaration", "type_item":
		return "type"
	case "const_item", "const_declaration":
		return "constant"
	default:
		return ""
	}
}

func treeSitterName(node *tree_sitter.Node, source []byte) string {
	if name := node.ChildByFieldName("name"); name != nil {
		return name.Utf8Text(source)
	}
	return firstTreeSitterIdentifier(node, source)
}

func firstTreeSitterIdentifier(node *tree_sitter.Node, source []byte) string {
	cursor := node.Walk()
	children := node.NamedChildren(cursor)
	cursor.Close()
	for _, child := range children {
		switch child.Kind() {
		case "identifier", "type_identifier", "property_identifier":
			return child.Utf8Text(source)
		}
		if name := firstTreeSitterIdentifier(&child, source); name != "" {
			return name
		}
	}
	return ""
}

func isTreeSitterImport(kind string) bool {
	switch kind {
	case "import_declaration", "import_statement", "import_from_statement", "preproc_include", "use_declaration", "require_once_expression", "require_expression", "include_expression", "include_once_expression":
		return true
	default:
		return false
	}
}

func treeSitterImport(node *tree_sitter.Node, source []byte) string {
	text := strings.TrimSpace(node.Utf8Text(source))
	for _, prefix := range []string{"import", "from", "use", "#include"} {
		text = strings.TrimPrefix(text, prefix)
	}
	return strings.Trim(strings.TrimSpace(strings.TrimRight(text, ";")), "\"'<> ")
}
