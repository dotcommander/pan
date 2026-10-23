package analyze

import (
	_ "embed"
	"fmt"
	"slices"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// Query assets are deliberately separate from traversal code: changing their
// evidence semantics requires changing AnalyzerRevision in model.go.
//
//go:embed queries/python-references.scm
var pythonReferenceQuery string

//go:embed queries/typescript-references.scm
var typescriptReferenceQuery string

// treeSitterAnalysis owns compiled queries for one sequential analysis pass.
// Queries are immutable during execution; cursors remain local to each file.
// Keeping ownership here avoids recompilation per file and global native state.
type treeSitterAnalysis struct {
	queries map[string]*tree_sitter.Query
}

func (a *treeSitterAnalysis) close() {
	for _, query := range a.queries {
		query.Close()
	}
	clear(a.queries)
}

func (a *treeSitterAnalysis) referenceQuery(language string) (*tree_sitter.Query, error) {
	if query := a.queries[language]; query != nil {
		return query, nil
	}
	querySource := ""
	switch language {
	case LanguagePython:
		querySource = pythonReferenceQuery
	case LanguageTypescript, LanguageJavascript, LanguageTsx, LanguageJsx:
		querySource = typescriptReferenceQuery
	default:
		return nil, nil
	}
	grammar := treeSitterLanguage(language)
	query, queryErr := tree_sitter.NewQuery(grammar, querySource)
	if queryErr != nil {
		return nil, fmt.Errorf("compile %s reference query: %w", language, queryErr)
	}
	if a.queries == nil {
		a.queries = make(map[string]*tree_sitter.Query)
	}
	a.queries[language] = query
	return query, nil
}

func (a *treeSitterAnalysis) queryReferences(root *tree_sitter.Node, source []byte, language string) ([]sourceReference, error) {
	query, err := a.referenceQuery(language)
	if err != nil || query == nil {
		return nil, err
	}
	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	captures := cursor.Captures(query, root, source)
	refs := make([]sourceReference, 0)
	seen := make(map[sourceReference]struct{})
	for match, captureIndex := captures.Next(); match != nil; match, captureIndex = captures.Next() {
		node := match.Captures[captureIndex].Node
		if insideImport(&node) {
			continue
		}
		ref := sourceReference{Name: node.Utf8Text(source), Line: int(node.StartPosition().Row) + 1, Column: int(node.StartPosition().Column) + 1}
		if ref.Name == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b sourceReference) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if a.Column != b.Column {
			return a.Column - b.Column
		}
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return refs, nil
}

func insideImport(node *tree_sitter.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "import_statement", "import_from_statement":
			return true
		}
	}
	return false
}
