package analyze

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func TestReferenceQueriesReuseWithinAnalysis(t *testing.T) {
	t.Parallel()
	analysis := treeSitterAnalysis{}
	t.Cleanup(analysis.close)
	for _, language := range []string{LanguagePython, LanguageTypescript, LanguageTsx, LanguageJavascript, LanguageJsx} {
		query, err := analysis.referenceQuery(language)
		if err != nil || query == nil {
			t.Fatalf("compile %s: query=%p err=%v", language, query, err)
		}
		again, err := analysis.referenceQuery(language)
		if err != nil || again != query {
			t.Fatalf("%s query was recompiled: %p != %p, err=%v", language, query, again, err)
		}
	}
	if analysis.queries[LanguageTypescript] == analysis.queries[LanguageTsx] {
		t.Fatal("different grammars must not share a compiled query")
	}
	if query, err := analysis.referenceQuery(LanguageRust); query != nil || err != nil {
		t.Fatalf("unsupported reference query: %p, %v", query, err)
	}
	analysis.close()
	if len(analysis.queries) != 0 {
		t.Fatal("closed native queries retained")
	}
	// Closing an analysis twice (including cleanup) must not double-free queries.
	analysis.close()
}

func TestNonSymbolNodeDoesNotReadDescendantNames(t *testing.T) {
	t.Parallel()
	parser := tree_sitter.NewParser()
	t.Cleanup(parser.Close)
	if err := parser.SetLanguage(treeSitterLanguage(LanguageTypescript)); err != nil {
		t.Fatal(err)
	}
	tree := parser.Parse([]byte("const value = target();"), nil)
	if tree == nil {
		t.Fatal("missing syntax tree")
	}
	t.Cleanup(tree.Close)
	root := tree.RootNode()
	if treeSitterSymbolKind(root.Kind()) != "" {
		t.Fatal("fixture root must not be a symbol")
	}
	// No source bytes are needed to reject a non-symbol node. A descendant
	// name lookup would access its identifier span and panic on this empty input.
	extractor := treeSitterExtractor{}
	extractor.addSymbol(root)
	if len(extractor.Symbols) != 0 {
		t.Fatalf("non-symbol produced declarations: %+v", extractor.Symbols)
	}
}
