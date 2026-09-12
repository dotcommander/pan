package improve

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// StructuralMetrics is the fee-eligible top-level declaration delta for a
// proposal. Lines remain a compatibility measure; fees use NetReduction.
type StructuralMetrics struct {
	SymbolsDeleted     int
	SymbolsAdded       int
	SymbolsModified    int
	NetSymbolReduction int
}

// Fee computes the nonnegative fee for a successful structural reduction.
func (m StructuralMetrics) Fee(success bool, feePerSymbol float64) float64 {
	if !success || m.NetSymbolReduction <= 0 || feePerSymbol <= 0 {
		return 0
	}
	return float64(m.NetSymbolReduction) * feePerSymbol
}

// MeasureStructuralProposal computes the source-compatible, fee-eligible
// declaration delta. Only Go files active in the current build context count;
// vendor, testdata, and generated sources never contribute to a payout.
func MeasureStructuralProposal(ctx context.Context, root string, changes []FileChange) (StructuralMetrics, error) {
	active, err := activeProposalGoFiles(ctx, root, changes)
	if err != nil {
		return StructuralMetrics{}, err
	}
	var metrics StructuralMetrics
	for _, change := range changes {
		fileChanges, err := structuralFileChanges(root, active, change)
		if err != nil {
			return StructuralMetrics{}, err
		}
		for _, change := range fileChanges {
			switch change.kind {
			case structuralAdded:
				metrics.SymbolsAdded++
			case structuralRemoved:
				metrics.SymbolsDeleted++
			case structuralModified:
				metrics.SymbolsModified++
			}
		}
	}
	metrics.NetSymbolReduction = metrics.SymbolsDeleted - metrics.SymbolsAdded
	return metrics, nil
}

func structuralFileChanges(root string, active map[string]bool, change FileChange) ([]structuralChange, error) {
	if !strings.HasSuffix(change.FilePath, ".go") {
		return nil, nil
	}
	full, err := SafeProposalPath(root, change.FilePath)
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(full)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read old %s: %w", change.FilePath, err)
	}
	if excludedFromStructuralMetrics(change.FilePath, string(old)) {
		return nil, nil
	}
	clean := filepath.Clean(filepath.FromSlash(change.FilePath))
	if len(old) > 0 && !active[clean] {
		return nil, nil
	}
	return structuralChanges(change.FilePath, string(old), change.NewContents)
}

func activeProposalGoFiles(ctx context.Context, root string, changes []FileChange) (map[string]bool, error) {
	directories, err := proposalGoDirectories(root, changes)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool)
	for _, directory := range directories {
		if err := addActiveGoFiles(ctx, root, directory, active); err != nil {
			return nil, err
		}
	}
	return active, nil
}

func proposalGoDirectories(root string, changes []FileChange) ([]string, error) {
	directories := make(map[string]bool)
	for _, change := range changes {
		if !strings.HasSuffix(change.FilePath, ".go") {
			continue
		}
		if hasProposalPathSegment(change.FilePath, "vendor") || hasProposalPathSegment(change.FilePath, "testdata") {
			continue
		}
		full, err := SafeProposalPath(root, change.FilePath)
		if err != nil {
			return nil, err
		}
		old, err := os.ReadFile(full)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read old %s: %w", change.FilePath, err)
		}
		if generatedGoSource(string(old)) {
			continue
		}
		if _, err := os.Stat(full); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("inspect old %s: %w", change.FilePath, err)
		}
		directories[filepath.Dir(full)] = true
	}
	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Strings(ordered)
	return ordered, nil
}

func addActiveGoFiles(ctx context.Context, root, directory string, active map[string]bool) error {
	files, err := listActiveGoFiles(ctx, directory)
	if err != nil {
		return err
	}
	for _, file := range files {
		rel, err := filepath.Rel(root, filepath.Join(directory, file))
		if err != nil {
			return fmt.Errorf("resolve active Go file %s: %w", file, err)
		}
		active[filepath.Clean(rel)] = true
	}
	return nil
}

func listActiveGoFiles(ctx context.Context, directory string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-json", ".")
	cmd.Dir = directory
	out, err := cmd.CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("list active Go files in %s: %w: %s", directory, err, strings.TrimSpace(string(out)))
	}
	var pkg struct {
		GoFiles  []string
		CgoFiles []string
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		return nil, fmt.Errorf("decode active Go files in %s: %w", directory, err)
	}
	return append(pkg.GoFiles, pkg.CgoFiles...), nil
}

func excludedFromStructuralMetrics(path, oldContents string) bool {
	return hasProposalPathSegment(path, "vendor") || hasProposalPathSegment(path, "testdata") || generatedGoSource(oldContents)
}

func hasProposalPathSegment(path, segment string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func generatedGoSource(contents string) bool {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if strings.HasPrefix(line, "// Code generated ") && strings.HasSuffix(line, " DO NOT EDIT.") {
			return true
		}
	}
	return false
}

type structuralChangeKind string

const (
	structuralAdded    structuralChangeKind = "added"
	structuralRemoved  structuralChangeKind = "removed"
	structuralModified structuralChangeKind = "modified"
)

type structuralChange struct {
	symbol string
	kind   structuralChangeKind
}

func structuralChanges(file, oldContents, newContents string) ([]structuralChange, error) {
	oldSymbols, err := structuralSymbols(file, oldContents)
	if err != nil {
		return nil, fmt.Errorf("parse old %s: %w", file, err)
	}
	newSymbols, err := structuralSymbols(file, newContents)
	if err != nil {
		return nil, fmt.Errorf("parse new %s: %w", file, err)
	}
	changes := make([]structuralChange, 0)
	for symbol, oldText := range oldSymbols {
		newText, exists := newSymbols[symbol]
		if !exists {
			changes = append(changes, structuralChange{symbol: symbol, kind: structuralRemoved})
		} else if oldText != newText {
			changes = append(changes, structuralChange{symbol: symbol, kind: structuralModified})
		}
	}
	for symbol := range newSymbols {
		if _, exists := oldSymbols[symbol]; !exists {
			changes = append(changes, structuralChange{symbol: symbol, kind: structuralAdded})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].symbol == changes[j].symbol {
			return changes[i].kind < changes[j].kind
		}
		return changes[i].symbol < changes[j].symbol
	})
	return changes, nil
}

func structuralSymbols(file, contents string) (map[string]string, error) {
	if strings.TrimSpace(contents) == "" {
		return map[string]string{}, nil
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, contents, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	symbols := make(map[string]string)
	for _, declaration := range parsed.Decls {
		addStructuralDeclaration(symbols, fset, declaration)
	}
	return symbols, nil
}

func addStructuralDeclaration(symbols map[string]string, fset *token.FileSet, declaration ast.Decl) {
	switch decl := declaration.(type) {
	case *ast.FuncDecl:
		symbols[structuralFuncKey(decl)] = structuralNodeText(fset, decl)
	case *ast.GenDecl:
		addStructuralGenDeclaration(symbols, fset, decl)
	}
}

func addStructuralGenDeclaration(symbols map[string]string, fset *token.FileSet, declaration *ast.GenDecl) {
	if declaration.Tok == token.IMPORT {
		return
	}
	for _, spec := range declaration.Specs {
		switch value := spec.(type) {
		case *ast.TypeSpec:
			symbols["type:"+value.Name.Name] = structuralNodeText(fset, value)
		case *ast.ValueSpec:
			addStructuralValueSymbols(symbols, fset, declaration.Tok, value)
		}
	}
}

func addStructuralValueSymbols(symbols map[string]string, fset *token.FileSet, tokenKind token.Token, value *ast.ValueSpec) {
	kind := "var:"
	if tokenKind == token.CONST {
		kind = "const:"
	}
	for _, name := range value.Names {
		if name.Name != "_" {
			symbols[kind+name.Name] = structuralNodeText(fset, value)
		}
	}
}

func structuralFuncKey(declaration *ast.FuncDecl) string {
	if declaration.Recv == nil || len(declaration.Recv.List) == 0 {
		return "func:" + declaration.Name.Name
	}
	return "method:" + structuralExprText(declaration.Recv.List[0].Type) + "." + declaration.Name.Name
}

func structuralExprText(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.StarExpr:
		return "*" + structuralExprText(value.X)
	case *ast.Ident:
		return value.Name
	case *ast.IndexExpr:
		return structuralExprText(value.X) + "[" + structuralExprText(value.Index) + "]"
	case *ast.IndexListExpr:
		parts := make([]string, len(value.Indices))
		for i, index := range value.Indices {
			parts[i] = structuralExprText(index)
		}
		return structuralExprText(value.X) + "[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprintf("%T", expression)
	}
}

func structuralNodeText(fset *token.FileSet, node ast.Node) string {
	var out strings.Builder
	if err := printer.Fprint(&out, fset, node); err != nil {
		return fmt.Sprintf("<print-error:%v>", err)
	}
	return out.String()
}
