package analyze

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// OutlineSymbols extracts one file's top-level symbols from in-memory
// content without touching the filesystem or applying any node budget; the
// caller owns bounds. It exists so evidence views over non-working-tree
// content (for example git object-store revisions) can reuse the same
// extractors as snapshot analysis. A parse failure returns the symbols
// recovered so far together with the error. Files whose language has no
// supported parser return no symbols and no error.
func OutlineSymbols(path string, contents []byte) ([]Symbol, error) {
	language := LanguageForPath(path)
	if language == LanguageGo {
		return outlineGoBytes(path, contents)
	}
	if SupportsTreeSitter(language) {
		parsed, err := parseTreeSitterBytes(contents, path, language)
		return parsed.Symbols, err
	}
	return nil, nil
}

// outlineGoBytes outlines one Go source buffer using the snapshot Go
// extractors. A parse error still returns recovered declarations, matching
// go/parser's partial-AST behavior.
func outlineGoBytes(rel string, contents []byte) ([]Symbol, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, contents, parser.ParseComments)
	if file == nil {
		return nil, err
	}
	pkg := file.Name.Name
	var symbols []Symbol
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			symbols = append(symbols, goFuncSymbol(fset, rel, pkg, d))
		case *ast.GenDecl:
			symbols = append(symbols, goGenSymbols(fset, rel, pkg, d)...)
		}
	}
	return symbols, err
}
