package scan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// RefactorReport groups exact normalized duplicate Go function bodies.
type RefactorReport struct {
	Groups []RefactorGroup `json:"groups"`
}

// RefactorGroup identifies duplicate bodies that may be an extraction lead.
type RefactorGroup struct {
	ID    string         `json:"id"`
	Kind  string         `json:"kind"`
	Sites []RefactorSite `json:"sites"`
}

// RefactorSite locates one duplicate function body.
type RefactorSite struct {
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type refactorKey struct{ pkg, kind, body string }

const refactorKindFunction = "function"

// RefactorSignatures finds exact normalized duplicate Go function bodies in
// the admitted snapshot. It is structural evidence, not a refactor verdict.
func RefactorSignatures(ctx context.Context, snap analyze.Snapshot) (RefactorReport, error) {
	buckets := map[refactorKey][]RefactorSite{}
	fset := token.NewFileSet()
	for _, item := range snap.Files {
		if item.Language != languageGo || item.Generated || isTestPath(item.Path) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return RefactorReport{}, err
		}
		if err := collectRefactorSites(item, snap.Root, fset, buckets); err != nil {
			return RefactorReport{}, err
		}
	}
	return refactorGroups(buckets), nil
}

func collectRefactorSites(item analyze.File, root string, fset *token.FileSet, buckets map[refactorKey][]RefactorSite) error {
	full := filepath.Join(root, filepath.FromSlash(item.Path))
	source, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("read refactor source %s: %w", item.Path, err)
	}
	file, err := parser.ParseFile(fset, full, source, 0)
	if err != nil {
		return fmt.Errorf("parse refactor source %s: %w", item.Path, err)
	}
	for _, decl := range file.Decls {
		if err := addRefactorDeclaration(item.Path, file.Name.Name, decl, fset, buckets); err != nil {
			return err
		}
	}
	return nil
}

func addRefactorDeclaration(path, pkg string, decl ast.Decl, fset *token.FileSet, buckets map[refactorKey][]RefactorSite) error {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Body == nil || len(fn.Body.List) < 5 {
		return nil
	}
	start, end := fset.Position(fn.Body.Lbrace).Line, fset.Position(fn.Body.Rbrace).Line
	if end-start+1 < 10 {
		return nil
	}
	var normalized bytes.Buffer
	if err := format.Node(&normalized, fset, fn.Body); err != nil {
		return fmt.Errorf("normalize refactor source %s: %w", path, err)
	}
	kind, symbol := refactorKindFunction, fn.Name.Name
	if fn.Recv != nil {
		kind, symbol = "method", receiverType(fn.Recv)+"."+symbol
	}
	key := refactorKey{pkg: filepath.ToSlash(filepath.Dir(path)) + ":" + pkg, kind: kind, body: normalized.String()}
	buckets[key] = append(buckets[key], RefactorSite{Path: path, Symbol: symbol, StartLine: start, EndLine: end})
	return nil
}

func refactorGroups(buckets map[refactorKey][]RefactorSite) RefactorReport {
	groups := make([]RefactorGroup, 0)
	for key, sites := range buckets {
		if len(sites) < 2 {
			continue
		}
		slices.SortFunc(sites, func(a, b RefactorSite) int {
			if a.Path != b.Path {
				return strings.Compare(a.Path, b.Path)
			}
			return a.StartLine - b.StartLine
		})
		sum := sha256.Sum256([]byte(key.pkg + "\x00" + key.kind + "\x00" + key.body))
		if len(sites) > 4 {
			sites = sites[:4]
		}
		groups = append(groups, RefactorGroup{ID: fmt.Sprintf("pan:refactor:%s:%x", key.kind, sum[:8]), Kind: key.kind, Sites: sites})
	}
	slices.SortFunc(groups, func(a, b RefactorGroup) int {
		if len(a.Sites) != len(b.Sites) {
			return len(b.Sites) - len(a.Sites)
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(groups) > 12 {
		groups = groups[:12]
	}
	return RefactorReport{Groups: groups}
}

func receiverType(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	var out bytes.Buffer
	_ = format.Node(&out, token.NewFileSet(), fields.List[0].Type)
	return out.String()
}
