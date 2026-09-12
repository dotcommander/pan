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

func queryReferences(root *tree_sitter.Node, source []byte, language string) ([]sourceReference, error) {
	querySource := ""
	switch language {
	case "python":
		querySource = pythonReferenceQuery
	case "typescript", "javascript", "tsx", "jsx":
		querySource = typescriptReferenceQuery
	default:
		return nil, nil
	}
	grammar := treeSitterLanguage(language)
	query, queryErr := tree_sitter.NewQuery(grammar, querySource)
	if queryErr != nil {
		return nil, fmt.Errorf("compile %s reference query: %w", language, queryErr)
	}
	defer query.Close()
	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	captures := cursor.Captures(query, root, source)
	refs := make([]sourceReference, 0)
	seen := make(map[sourceReference]struct{})
	for match, captureIndex := captures.Next(); match != nil; match, captureIndex = captures.Next() {
		node := match.Captures[captureIndex].Node
		ref := sourceReference{Name: node.Utf8Text(source), Line: int(node.StartPosition().Row) + 1}
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
