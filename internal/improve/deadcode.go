package improve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProposalSource identifies the deterministic proposal lane.
const ProposalSource = "deterministic-deadcode"

// DeadSymbol is one unexported top-level declaration with zero lexical
// references anywhere in the module (tests included). Deletion candidates
// are found by name counting, so a name referenced anywhere — including
// coincidentally, as a field or method on another type — is never proposed.
// This is deliberately conservative and explicitly lexical, matching pan's
// documented lower-confidence by-name evidence class.
type DeadSymbol struct {
	File string `json:"file"`
	Name string `json:"name"`
	Kind string `json:"kind"` // func | method | type | const | var
	Line int    `json:"line"`
}

// Declaration kinds reported by the detector; a declaration kind string is
// part of the improve report schema.
const (
	kindFunc   = "func"
	kindMethod = "method"
	kindType   = "type"
	kindConst  = "const"
	kindVar    = "var"
)

// testPrefix marks Go test functions, which are never deletion candidates.
const testPrefix = "Test"

const entryPointMain = "main"

// isNeverDelete reports whether name is an entry point or compiler
// mandated, regardless of reference counts.
func isNeverDelete(name string) bool {
	switch name {
	case entryPointMain, "init", "_":
		return true
	}
	return false
}

// DetectDeadSymbols walks root's non-test Go files (honoring exclude,
// slash-relative, rooted at the walk root) and returns the unexported
// top-level declarations with zero references in any Go file, tests
// included. Results are sorted by file then name for determinism.
func DetectDeadSymbols(root string, exclude []string) ([]DeadSymbol, error) {
	files, err := listGoFiles(root, exclude)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	index, err := indexModule(files, fset)
	if err != nil {
		return nil, err
	}
	dead := collectDeadSymbols(root, files, fset, index)
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].File != dead[j].File {
			return dead[i].File < dead[j].File
		}
		return dead[i].Name < dead[j].Name
	})
	return dead, nil
}

// moduleIndex is the whole-module lexical index behind dead-symbol
// detection: every parsed file plus identifier occurrence and top-level
// declaration counts.
type moduleIndex struct {
	parsed       map[string]*ast.File
	references   map[string]int
	declarations map[string]int
}

// indexModule parses every file and counts identifier occurrences and
// top-level declarations across the module in one pass.
func indexModule(files []string, fset *token.FileSet) (*moduleIndex, error) {
	index := &moduleIndex{
		parsed:       make(map[string]*ast.File, len(files)),
		references:   make(map[string]int),
		declarations: make(map[string]int),
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return nil, err
		}
		index.parsed[path] = file
		countReferences(file, index.references)
		for _, decl := range file.Decls {
			for _, candidate := range declCandidates(decl, dummyCandidate) {
				index.declarations[candidate.Name]++
			}
		}
	}
	return index, nil
}

// collectDeadSymbols returns the dead declarations of every non-test file,
// in file-then-name order. A name is dead only when every occurrence in
// the module is a declaration site: zero real references anywhere, tests
// included. Lexical counting is deliberately conservative.
func collectDeadSymbols(root string, files []string, fset *token.FileSet, index *moduleIndex) []DeadSymbol {
	var dead []DeadSymbol
	for _, path := range files {
		rel := relSlash(root, path)
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		for _, decl := range index.parsed[path].Decls {
			for _, candidate := range declCandidates(decl, dummyCandidate) {
				if index.references[candidate.Name] == index.declarations[candidate.Name] {
					dead = append(dead, DeadSymbol{
						File: rel, Name: candidate.Name, Kind: candidate.Kind,
						Line: fset.Position(token.Pos(candidate.Line)).Line,
					})
				}
			}
		}
	}
	return dead
}

// dummyCandidate is the declCandidates callback used when only names and
// positions are needed.
func dummyCandidate(line int, name, kind string) DeadSymbol {
	return DeadSymbol{Name: name, Kind: kind, Line: line}
}

// declCandidates yields the deletable unexported declarations of one
// top-level decl via the build callback.
func declCandidates(decl ast.Decl, build func(line int, name, kind string) DeadSymbol) []DeadSymbol {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return funcDeclCandidates(d, build)
	case *ast.GenDecl:
		return genDeclCandidates(d, build)
	}
	return nil
}

// funcDeclCandidates yields the deletable declarations of one function or
// method decl. Methods are addressed by name; the receiver survives
// because its type is only dead when the type itself is.
func funcDeclCandidates(d *ast.FuncDecl, build func(line int, name, kind string) DeadSymbol) []DeadSymbol {
	name := d.Name.Name
	if d.Recv != nil {
		if deletableName(name) {
			return []DeadSymbol{build(int(d.Name.Pos()), name, kindMethod)}
		}
		return nil
	}
	if deletableName(name) && !strings.HasPrefix(name, testPrefix) {
		return []DeadSymbol{build(int(d.Name.Pos()), name, kindFunc)}
	}
	return nil
}

// genDeclCandidates yields the deletable declarations of one type, const,
// or var decl; import decls are never candidates.
func genDeclCandidates(d *ast.GenDecl, build func(line int, name, kind string) DeadSymbol) []DeadSymbol {
	if d.Tok == token.IMPORT {
		return nil
	}
	kind := genDeclKind(d.Tok)
	if kind == "" {
		return nil
	}
	var out []DeadSymbol
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if deletableName(s.Name.Name) {
				out = append(out, build(int(s.Name.Pos()), s.Name.Name, kind))
			}
		case *ast.ValueSpec:
			for _, id := range s.Names {
				if deletableName(id.Name) {
					out = append(out, build(int(id.Pos()), id.Name, kind))
				}
			}
		}
	}
	return out
}

// genDeclKind maps a gen-decl token to its report kind; import and any
// other token yields "". An if chain keeps this off the exhaustive
// token.Token switch/map contract, which would demand every token case.
func genDeclKind(tok token.Token) string {
	if tok == token.TYPE {
		return kindType
	}
	if tok == token.CONST {
		return kindConst
	}
	if tok == token.VAR {
		return kindVar
	}
	return ""
}

// deletableName reports whether name is an unexported, non-entry-point
// identifier.
func deletableName(name string) bool {
	if name == "" || isNeverDelete(name) {
		return false
	}
	first, _ := utf8.DecodeRuneInString(name)
	return !unicode.IsUpper(first)
}

// countReferences adds every identifier occurrence (including selector
// suffixes and struct field names — deliberately conservative) to counts.
func countReferences(file *ast.File, counts map[string]int) {
	ast.Inspect(file, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name != "_" && ident.Name != "." {
			counts[ident.Name]++
		}
		return true
	})
}

// listGoFiles returns every .go file under root, excluding configured
// directories, sorted for determinism.
func listGoFiles(root string, exclude []string) ([]string, error) {
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		skip[filepath.ToSlash(filepath.Clean(e))] = true
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := relSlash(root, path)
		if d.IsDir() {
			if rel != "." && skip[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
