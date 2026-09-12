package scan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// accessReadWrite marks a store that is both read and written.
const accessReadWrite = "r/w"

// ─── detectStores ─────────────────────────────────────────────────────────────

// detectStores scans all Go files across all phases for persistent-state
// patterns and returns the deduplicated Store list plus the phases with
// embedded non-Go files added to their Files lists.
//
// Detected patterns:
//   - //go:embed directives → read-only source files added to their phase
//   - os.ReadFile(path)     → Store, access "r"
//   - os.WriteFile(path,..) → Store, access "w"
//   - os.OpenFile(path, flags, ..) → Store, access derived from flags
//   - os.MkdirAll(path, ..)  → Store, access "w"
//   - os.Rename(_, path)     → Store, access "w" (atomic replacement)
//   - persistence imports (database/sql, pgx, …) → datastore Store, access "r/w"
func detectStores(root string, phases []spec.Phase) ([]spec.Store, []spec.Phase) {
	scan := newStoreScan(root, phases)
	for _, rel := range scan.allFiles() {
		scan.scanFile(rel)
	}
	return flattenStores(scan.storeMap), scan.out
}

// storeScan carries the mutable state of one detectStores pass: the phases
// being augmented with embedded files, the chip-label lookup, the writer
// accumulation keyed by store name, and the file→phase index.
type storeScan struct {
	root      string
	phases    []spec.Phase
	out       []spec.Phase
	labels    map[string]bool
	storeMap  map[string][]spec.Writer
	filePhase map[string]int
}

// newStoreScan prepares the scan state over the given phases.
func newStoreScan(root string, phases []spec.Phase) *storeScan {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)
	filePhase := make(map[string]int)
	for pi, p := range phases {
		for _, f := range p.Files {
			filePhase[f] = pi
		}
	}
	return &storeScan{
		root:      root,
		phases:    phases,
		out:       out,
		labels:    chipLabels(phases),
		storeMap:  make(map[string][]spec.Writer),
		filePhase: filePhase,
	}
}

// allFiles returns the unique source files across every phase in first-seen
// order.
func (s *storeScan) allFiles() []string {
	seen := make(map[string]bool)
	var allFiles []string
	for _, p := range s.phases {
		for _, f := range p.Files {
			if !seen[f] {
				seen[f] = true
				allFiles = append(allFiles, f)
			}
		}
	}
	return allFiles
}

// scanFile applies every store pattern to one source file: embed directives,
// os-package call sites, and persistence imports.
func (s *storeScan) scanFile(rel string) {
	abs := filepath.Join(s.root, rel)

	// Pattern 1: //go:embed — scan raw bytes (AST drops these).
	_, embedPaths := detectEmbeds(abs, s.root)
	s.addEmbedPaths(rel, embedPaths)

	// Patterns 2-4: AST-based.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, 0)
	if err != nil {
		return
	}
	ast.Inspect(f, (&storeVisitor{scan: s, fset: fset, file: f, rel: rel}).Visit)

	// Pattern 5: persistence-layer imports → datastore Store.
	s.scanImports(fset, f, rel)
}

// addEmbedPaths adds embedded non-Go files to the source file's phase.
func (s *storeScan) addEmbedPaths(rel string, embedPaths []string) {
	if len(embedPaths) == 0 {
		return
	}
	pi, ok := s.filePhase[rel]
	if !ok {
		return
	}
	for _, ep := range embedPaths {
		if filepath.Ext(ep) == ".go" {
			continue
		}
		if !slices.Contains(s.out[pi].Files, ep) {
			s.out[pi].Files = append(s.out[pi].Files, ep)
		}
	}
}

// scanImports attributes persistence-signal imports to a datastore Store on
// the importing file's phase. Reuses the store-signal config table.
func (s *storeScan) scanImports(fset *token.FileSet, f *ast.File, rel string) {
	pi, ok := s.filePhase[rel]
	if !ok {
		return
	}
	signals := storeSignals()
	for _, imp := range f.Imports {
		ipath := strings.Trim(imp.Path.Value, `"`)
		for signal, storeName := range signals {
			if !strings.HasPrefix(ipath, signal) {
				continue
			}
			s.storeMap[storeName] = append(s.storeMap[storeName], spec.Writer{
				Stage:      s.phases[pi].Name,
				Access:     accessReadWrite,
				Note:       "persistence",
				SourceFile: rel,
				SourceLine: fset.Position(imp.Pos()).Line,
			})
		}
	}
}

// storeVisitor records os-package call sites as store writers during one
// AST inspection.
type storeVisitor struct {
	scan *storeScan
	fset *token.FileSet
	file *ast.File
	rel  string
}

// Visit records os-package store calls; every other node descends.
func (v *storeVisitor) Visit(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return true
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return true
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "os" {
		return true
	}
	v.recordOSCall(call, sel.Sel.Name)
	return true
}

// recordOSCall dispatches one os-package call site to its store pattern.
func (v *storeVisitor) recordOSCall(call *ast.CallExpr, name string) {
	switch name {
	case "ReadFile":
		v.recordPathStore(call, 0, "r", "read")
	case "WriteFile":
		v.recordPathStore(call, 0, "w", "write")
	case "OpenFile":
		v.recordOpenFile(call)
	case "CreateTemp":
		v.recordCreateTemp(call)
	case "MkdirAll":
		v.recordPathStore(call, 0, "w", "directory")
	case "Rename":
		v.recordPathStore(call, 1, "w", "atomic replace")
	}
}

// recordPathStore records a call whose argument at argIdx names a store
// path, when the path resolves to a non-empty store name.
func (v *storeVisitor) recordPathStore(call *ast.CallExpr, argIdx int, access, note string) {
	if len(call.Args) <= argIdx {
		return
	}
	storeName := v.storeNameFor(call, call.Args[argIdx])
	if storeName == "" {
		return
	}
	v.record(storeName, v.writer(call, call.Args[argIdx], access, note))
}

// recordOpenFile records os.OpenFile with an access mode derived from its
// flag argument.
func (v *storeVisitor) recordOpenFile(call *ast.CallExpr) {
	if len(call.Args) < 2 {
		return
	}
	storeName := v.storeNameFor(call, call.Args[0])
	if storeName == "" {
		return
	}
	access := openFileAccess(call.Args[1])
	note := openFileNote(access, call.Args[1])
	v.record(storeName, v.writer(call, call.Args[0], access, note))
}

// recordCreateTemp records os.CreateTemp as a write to a temp-file store
// named by its pattern argument when literal.
func (v *storeVisitor) recordCreateTemp(call *ast.CallExpr) {
	storeName := "temp file"
	var pathExpr ast.Expr
	if len(call.Args) >= 2 {
		pathExpr = call.Args[1]
		if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s := lit.Value
			if len(s) >= 2 {
				s = s[1 : len(s)-1]
			}
			storeName = s
		}
	}
	v.record(storeName, v.writer(call, pathExpr, "w", "temp file"))
}

// storeNameFor resolves a store-path argument inside the enclosing function
// and returns its store name, or "" when it cannot be resolved.
func (v *storeVisitor) storeNameFor(call *ast.CallExpr, pathExpr ast.Expr) string {
	enclosing := findEnclosingFunc(v.file, call.Pos())
	return fileStoreName(resolvedStorePathName(v.file, pathExpr, enclosing))
}

// writer builds one writer record for a call site.
func (v *storeVisitor) writer(call *ast.CallExpr, pathExpr ast.Expr, access, note string) spec.Writer {
	return spec.Writer{
		Stage:         v.stageLabel(call),
		Access:        access,
		Note:          note,
		SourceFile:    v.rel,
		SourceLine:    v.fset.Position(call.Pos()).Line,
		SourceSymbols: storeSourceSymbols(pathExpr),
	}
}

// stageLabel maps the call's enclosing function to its chip label or phase
// name.
func (v *storeVisitor) stageLabel(call *ast.CallExpr) string {
	phaseName := ""
	if pi, ok := v.scan.filePhase[v.rel]; ok {
		phaseName = v.scan.phases[pi].Name
	}
	enclosing := findEnclosingFunc(v.file, call.Pos())
	return mapToStageLabel(enclosing, v.scan.labels, phaseName)
}

// record appends one writer to its store's accumulation.
func (v *storeVisitor) record(storeName string, writer spec.Writer) {
	v.scan.storeMap[storeName] = append(v.scan.storeMap[storeName], writer)
}
