// Package symbols builds a deterministic, Go-AST-only index of the symbols,
// imports, and packages in a module tree. It is the static-analysis seam for
// the pipeline packages: scan, review, and storyboard consume RankedFile
// values and never touch the filesystem themselves.
//
// Explicit limitations, by design:
//   - Go source only. Non-Go files are invisible to the index.
//   - Syntax-only analysis: no type checking, no build constraints, no
//     cross-module resolution. Build-tag-excluded files are indexed like any
//     other file.
//   - Ranking is deterministic (path order), not importance-ranked; callers
//     that need importance order must derive it themselves.
//   - Hidden directories, vendor, node_modules, and testdata are skipped.
package symbols

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// scanMaxFileSizeDefault caps single-file parsing when Config.MaxFileSize is
// zero. Files above the cap are skipped, never truncated.
const scanMaxFileSizeDefault = 1_000_000

// skipDir reports whether a directory is never walked. testdata is skipped
// because fixtures are not pipeline code; vendor and node_modules are
// third-party trees.
func skipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "vendor", "node_modules", "testdata":
		return true
	}
	return false
}

// Config controls index construction. MaxFileSize <= 0 applies the default
// cap; a negative value disables the cap. MaxTokens is accepted for call-site
// compatibility and ignored: the index is never token-budgeted.
type Config struct {
	MaxTokens   int
	MaxFileSize int
}

// Symbol is one declared symbol in a file.
type Symbol struct {
	Name        string // e.g. "Agent", "New", "Run"
	Kind        string // "function", "method", "struct", "interface", "constant", "variable", "type"
	Signature   string // e.g. "(ctx, provider, opts) *Agent" — params + return, no func keyword
	Receiver    string // e.g. "*Agent" — methods only, empty for functions
	Exported    bool   // true if the symbol name is exported
	Line        int    // 1-based source line number (0 = unknown)
	EndLine     int    // 1-based end line number (0 = unknown, same as Line when unavailable)
	ParamCount  int    // parameter count (funcs/methods); 0 otherwise
	ResultCount int    // return value count (funcs/methods); 0 otherwise
	Doc         string // first sentence of the doc comment (empty if none)
}

// LineSpan returns the symbol's source span in lines. Files whose parser
// recorded no end position report 0, matching the unknown-EndLine convention.
func (s Symbol) LineSpan() int {
	if s.EndLine <= 0 || s.Line <= 0 {
		return 0
	}
	return s.EndLine - s.Line + 1
}

// FileSymbols is the parsed content of one source file.
type FileSymbols struct {
	Path        string // repository-relative path, slash-separated
	Language    string // always "go"
	Package     string // package clause name
	ImportPath  string // module-qualified import path ("" when no go.mod is found)
	Symbols     []Symbol
	Imports     []string // imported package paths as written
	ParseMethod string   // always "go_ast"
}

// RankedFile couples one file's symbols with deterministic rank metadata.
// The embedded pointer is never nil for files returned by Map.Ranked.
type RankedFile struct {
	*FileSymbols
	ImportedBy int // number of indexed files importing this file's ImportPath
}

// Map is a built symbol index. Build must be called exactly once before
// Ranked; the zero Map is not usable.
type Map struct {
	root    string
	cfg     Config
	module  string
	files   []*FileSymbols
	ranked  []RankedFile
	built   bool
	skipped []string
}

// New returns an unbuilt index over root. Call Build, then Ranked.
func New(root string, cfg Config) *Map {
	return &Map{root: root, cfg: cfg}
}

// Build walks the tree under the configured root, parses every Go file under
// the size cap, and finalizes the index. It is idempotent per Map: a second
// call returns nil without rewalking.
func (m *Map) Build(_ context.Context) error {
	if m.built {
		return nil
	}
	absRoot, err := filepath.Abs(m.root)
	if err != nil {
		return fmt.Errorf("symbols: resolve root: %w", err)
	}
	m.root = absRoot
	m.module = readModulePath(absRoot)
	if err := m.walk(absRoot); err != nil {
		return fmt.Errorf("symbols: walk: %w", err)
	}
	sort.Slice(m.files, func(i, j int) bool { return m.files[i].Path < m.files[j].Path })
	m.ranked = rankByImports(m.files)
	m.built = true
	return nil
}

// walk walks the tree under root, delegating each entry to visit.
func (m *Map) walk(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		return m.visit(path, d, err)
	})
}

// visit classifies one walk entry. Walk errors and non-Go files are skipped;
// directories are pruned per skipDir; Go files are visited under the size
// cap. Skipping records the path and continues the walk rather than aborting.
func (m *Map) visit(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return m.skip(path)
	}
	if d.IsDir() {
		return m.visitDir(path, d)
	}
	if !strings.HasSuffix(path, ".go") {
		return nil
	}
	return m.visitFile(path, d)
}

// visitDir prunes hidden and third-party directories, never the root itself.
func (m *Map) visitDir(path string, d fs.DirEntry) error {
	if path != m.root && (skipDir(d.Name()) || strings.HasPrefix(d.Name(), ".")) {
		return filepath.SkipDir
	}
	return nil
}

// visitFile indexes one Go file under the configured size cap. Stat and
// parse failures are recorded as skips, never as walk aborts.
func (m *Map) visitFile(path string, d fs.DirEntry) error {
	info, statErr := d.Info()
	if statErr != nil {
		return m.skip(path)
	}
	if m.maxFileSize() > 0 && info.Size() > int64(m.maxFileSize()) {
		return m.skip(path)
	}
	file, parseErr := parseFile(m.root, m.module, path)
	if parseErr != nil {
		return m.skip(path)
	}
	if file != nil {
		m.files = append(m.files, file)
	}
	return nil
}

// skip records one skipped path and continues the walk.
func (m *Map) skip(path string) error {
	m.skipped = append(m.skipped, path)
	return nil
}

// maxFileSize resolves the single-file parse cap: zero applies the default
// cap, a negative value disables the cap.
func (m *Map) maxFileSize() int {
	if m.cfg.MaxFileSize == 0 {
		return scanMaxFileSizeDefault
	}
	return m.cfg.MaxFileSize
}

// Ranked returns every indexed file with rank metadata, sorted by path.
// The returned slice belongs to the Map; callers must not mutate it.
func (m *Map) Ranked() []RankedFile {
	return m.ranked
}

// Skipped returns the paths skipped by the walk, sorted. Parse failures and
// oversize files are reported here rather than dropped silently.
func (m *Map) Skipped() []string {
	out := append([]string(nil), m.skipped...)
	sort.Strings(out)
	return out
}

// rankByImports computes ImportedBy counts and returns the path-sorted
// ranked view.
func rankByImports(files []*FileSymbols) []RankedFile {
	importers := make(map[string]int, len(files))
	for _, f := range files {
		for _, imp := range f.Imports {
			importers[imp]++
		}
	}
	out := make([]RankedFile, 0, len(files))
	for _, f := range files {
		out = append(out, RankedFile{FileSymbols: f, ImportedBy: importers[f.ImportPath]})
	}
	return out
}

// readModulePath returns the module path declared in dir's go.mod, or "".
func readModulePath(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if mod, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(strings.Trim(strings.TrimSpace(mod), `"`))
		}
	}
	return ""
}

// parseFile parses one Go file into FileSymbols. parse errors yield (nil, err)
// so callers can record the skip; files with no declarations still index.
func parseFile(absRoot, module, path string) (*FileSymbols, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	rel, relErr := filepath.Rel(absRoot, path)
	if relErr != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)

	fs := &FileSymbols{
		Path:        rel,
		Language:    "go",
		Package:     file.Name.Name,
		ImportPath:  importPathFor(module, filepath.ToSlash(filepath.Dir(rel))),
		ParseMethod: "go_ast",
	}
	for _, imp := range file.Imports {
		fs.Imports = append(fs.Imports, strings.Trim(imp.Path.Value, `"`))
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			fs.Symbols = append(fs.Symbols, funcSymbol(fset, d))
		case *ast.GenDecl:
			fs.Symbols = append(fs.Symbols, genSymbols(fset, d)...)
		}
	}
	return fs, nil
}

// importPathFor joins the module path with a slash dir. Root dirs and empty
// modules yield the plain module / empty string.
func importPathFor(module, dir string) string {
	switch {
	case module == "":
		return ""
	case dir == "." || dir == "":
		return module
	default:
		return module + "/" + dir
	}
}

func funcSymbol(fset *token.FileSet, fn *ast.FuncDecl) Symbol {
	sym := Symbol{
		Name:        fn.Name.Name,
		Kind:        "function",
		Signature:   signature(fset, fn.Type),
		Exported:    fn.Name.IsExported(),
		Line:        fset.Position(fn.Pos()).Line,
		EndLine:     fset.Position(fn.End()).Line,
		ParamCount:  countFields(fn.Type.Params),
		ResultCount: countFields(fn.Type.Results),
		Doc:         firstSentence(fn.Doc),
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sym.Kind = "method"
		sym.Receiver = typeString(fset, fn.Recv.List[0].Type)
	}
	return sym
}

func genSymbols(fset *token.FileSet, decl *ast.GenDecl) []Symbol {
	var out []Symbol
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			kind := "type"
			switch s.Type.(type) {
			case *ast.StructType:
				kind = "struct"
			case *ast.InterfaceType:
				kind = "interface"
			}
			out = append(out, Symbol{
				Name:     s.Name.Name,
				Kind:     kind,
				Exported: s.Name.IsExported(),
				Line:     fset.Position(s.Pos()).Line,
				EndLine:  fset.Position(s.End()).Line,
				Doc:      firstSentence(decl.Doc),
			})
		case *ast.ValueSpec:
			kind := "variable"
			if decl.Tok.String() == "const" {
				kind = "constant"
			}
			for _, name := range s.Names {
				out = append(out, Symbol{
					Name:     name.Name,
					Kind:     kind,
					Exported: name.IsExported(),
					Line:     fset.Position(s.Pos()).Line,
					EndLine:  fset.Position(s.End()).Line,
					Doc:      firstSentence(decl.Doc),
				})
			}
		}
	}
	return out
}

// countFields counts AST fields, not names: "a, b int" is one field. This
// matches the ParamCount convention used by the pipeline consumers, which
// treat it as an arity signal rather than an exact parameter count.
func countFields(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	return fields.NumFields()
}

// signature renders "(name, name) type, type" from a function type. Parameter
// names are preferred over types; unnamed parameters fall back to types.
func signature(fset *token.FileSet, typ *ast.FuncType) string {
	var params []string
	if typ.Params != nil {
		for _, field := range typ.Params.List {
			if len(field.Names) > 0 {
				for _, name := range field.Names {
					params = append(params, name.Name)
				}
			} else {
				params = append(params, typeString(fset, field.Type))
			}
		}
	}
	var results []string
	if typ.Results != nil {
		for _, field := range typ.Results.List {
			results = append(results, typeString(fset, field.Type))
		}
	}
	out := "(" + strings.Join(params, ", ") + ")"
	if len(results) > 0 {
		if len(results) == 1 {
			out += " " + results[0]
		} else {
			out += " (" + strings.Join(results, ", ") + ")"
		}
	}
	return out
}

// typeString renders an expression's source text through the printer so it
// matches how the file was parsed, independent of original formatting.
func typeString(fset *token.FileSet, expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return ""
	}
	return buf.String()
}

// firstSentence returns the doc comment text up to the first sentence break.
func firstSentence(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	text := strings.TrimSpace(doc.Text())
	if text == "" {
		return ""
	}
	if idx := strings.IndexAny(text, ".\n"); idx >= 0 {
		return strings.TrimSpace(text[:idx+1])
	}
	return text
}
