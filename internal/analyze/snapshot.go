package analyze

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/config"
)

// Skip reasons recorded in Status.Skipped entries formatted "<path> (<reason>)".
const (
	reasonExcluded  = "excluded"
	reasonSymlink   = "symlink"
	reasonIrregular = "irregular"
	reasonOversized = "oversized"
	reasonStatError = "stat-error"
	reasonWalkError = "walk-error"
)

// Limit reasons recorded in Status.Limits when a bound truncates discovery.
const (
	limitMaxFiles      = "max_files"
	limitMaxFileBytes  = "max_file_bytes"
	limitMaxTotalBytes = "max_total_bytes"
	limitMaxNodes      = "max_nodes"
)

// Bounds on reported diagnostics and skipped entries keep the snapshot itself
// bounded regardless of repository shape. Totals are preserved via
// Status.SkippedCount and a trailing sentinel entry.
const (
	maxSkippedReported = 100
	maxDiagnosticsKept = 100
)

// Build walks root within cfg's bounds and returns a finalized snapshot.
// Discovery is bounded by file count, per-file size, cumulative size, and
// graph nodes; every truncation is reported explicitly through Status.Limits,
// and every skipped path through Status.Skipped. The result is deterministic:
// two runs over the same tree produce deeply equal snapshots.
func Build(ctx context.Context, root string, cfg config.Config) (Snapshot, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Snapshot{}, fmt.Errorf("repository root must be a directory: %s", root)
	}
	before, err := Stamps(ctx, root, cfg)
	if err != nil {
		return Snapshot{}, err
	}
	b := &builder{ctx: ctx, cfg: cfg.Normalized(), complete: true}
	b.snap = Snapshot{Root: root, Captured: make(map[string][]byte)}
	if err := filepath.WalkDir(root, b.visit); err != nil {
		return Snapshot{}, fmt.Errorf("walk repository: %w", err)
	}
	out := b.finalize()
	after, err := Stamps(ctx, root, cfg)
	if err != nil {
		return Snapshot{}, err
	}
	if changedStamps(before, after) {
		return Snapshot{}, fmt.Errorf("repository_changed_during_capture")
	}
	out.CapturedStamps = after
	return out, nil
}

func changedStamps(a, b map[string]FileStamp) bool {
	if len(a) != len(b) {
		return true
	}
	for p, x := range a {
		y, ok := b[p]
		if !ok || x.Size != y.Size || !x.ModTime.Equal(y.ModTime) {
			return true
		}
	}
	return false
}

// builder accumulates one analysis pass. It is confined to Build and its
// methods; only the finalized Snapshot escapes.
type builder struct {
	ctx        context.Context
	cfg        config.Config
	snap       Snapshot
	total      int64
	nodes      int
	complete   bool
	limits     []string
	skipped    []string
	diags      []Diagnostic
	references map[string][]sourceReference
}

func (b *builder) visit(path string, d fs.DirEntry, walkErr error) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	rel, err := filepath.Rel(b.snap.Root, path)
	if err != nil {
		return err
	}
	slashRel := filepath.ToSlash(rel)
	if walkErr != nil {
		if rel == "." {
			return walkErr
		}
		b.skip(slashRel, reasonWalkError)
		return nil
	}
	if rel == "." {
		return nil
	}
	if excluded(slashRel, b.cfg.Exclude) {
		b.skip(slashRel, reasonExcluded)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if d.IsDir() {
		return nil
	}
	if d.Type()&fs.ModeSymlink != 0 {
		b.skip(slashRel, reasonSymlink)
		return nil
	}
	if !d.Type().IsRegular() {
		b.skip(slashRel, reasonIrregular)
		return nil
	}
	if len(b.snap.Files) >= b.cfg.MaxFiles {
		b.limit(limitMaxFiles)
		return fs.SkipAll
	}
	info, ok := entryInfo(d)
	if !ok {
		b.skip(slashRel, reasonStatError)
		return nil
	}
	if info.Size() > b.cfg.MaxFileBytes {
		b.skip(slashRel, reasonOversized)
		b.limit(limitMaxFileBytes)
		return nil
	}
	if b.total+info.Size() > b.cfg.MaxTotalBytes {
		b.limit(limitMaxTotalBytes)
		return fs.SkipAll
	}
	b.total += info.Size()
	file := File{Path: slashRel, Language: language(rel), Size: info.Size(), Generated: generated(path)}
	b.snap.Files = append(b.snap.Files, file)
	contents, readErr := os.ReadFile(path)
	if readErr != nil {
		return fmt.Errorf("read %s: %w", slashRel, readErr)
	}
	b.snap.Captured[file.Path] = append([]byte(nil), contents...)
	b.parseSource(path, file, contents)
	return nil
}

func (b *builder) parseSource(absPath string, file File, contents []byte) {
	if file.Language == languageGo {
		b.parseGoBytes(contents, file.Path)
		return
	}
	parsed, err := parseTreeSitterBytes(contents, file.Path, file.Language)
	if err != nil {
		b.diag(Diagnostic{Level: diagnosticWarning, Message: err.Error(), Location: &Location{Path: file.Path}})
		return
	}
	for _, symbol := range parsed.Symbols {
		b.addSymbol(symbol)
	}
	for _, imported := range parsed.Imports {
		b.addEdge(Edge{From: file.Path, To: imported, Kind: "imports", Confidence: ConfidenceSyntactic, Location: Location{Path: file.Path, Line: 1}})
	}
	if len(parsed.References) > 0 {
		if b.references == nil {
			b.references = make(map[string][]sourceReference)
		}
		b.references[file.Path] = append(b.references[file.Path], parsed.References...)
	}
}

func (b *builder) parseGoBytes(contents []byte, rel string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, contents, parser.ParseComments)
	if err != nil {
		b.complete = false
		b.diag(Diagnostic{Level: diagnosticWarning, Message: err.Error(), Location: &Location{Path: rel}})
		return
	}
	pkg := file.Name.Name
	for _, imp := range file.Imports {
		b.addEdge(Edge{From: rel, To: strings.Trim(imp.Path.Value, "\""), Kind: "imports", Confidence: ConfidenceSyntactic, Location: locate(fset, imp.Pos(), rel)})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			b.addSymbol(goFuncSymbol(fset, rel, pkg, d))
			b.addCallEdges(fset, rel, d)
		case *ast.GenDecl:
			for _, symbol := range goGenSymbols(fset, rel, pkg, d) {
				b.addSymbol(symbol)
			}
		}
	}
}

// addCallEdges records lexical call edges from one function body. Call edges
// match by bare callee name, so they are lower-confidence evidence than the
// semantic import edges recorded from file.Imports.
func (b *builder) addCallEdges(fset *token.FileSet, rel string, fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := callName(call.Fun); name != "" {
			b.addEdge(Edge{From: fn.Name.Name, To: name, Kind: "calls", Confidence: "lexical", Location: locate(fset, call.Pos(), rel)})
		}
		return true
	})
}

func (b *builder) addSymbol(symbol Symbol) {
	if b.nodeFull() {
		return
	}
	b.snap.Symbols = append(b.snap.Symbols, symbol)
	b.nodes++
}

func (b *builder) addEdge(edge Edge) {
	if b.nodeFull() {
		return
	}
	b.snap.Edges = append(b.snap.Edges, edge)
	b.nodes++
}

func (b *builder) nodeFull() bool {
	if b.nodes >= b.cfg.MaxNodes {
		b.limit(limitMaxNodes)
		return true
	}
	return false
}

func (b *builder) skip(rel, reason string) {
	b.skipped = append(b.skipped, rel+" ("+reason+")")
	if reason == reasonWalkError || reason == reasonStatError {
		b.complete = false
	}
}

func (b *builder) limit(reason string) {
	b.limits = append(b.limits, reason)
	b.complete = false
}

func (b *builder) diag(d Diagnostic) {
	b.diags = append(b.diags, d)
}

// finalize sorts, dedupes, and caps all accumulated state into an immutable
// snapshot. It must be called at most once, after the walk completes.
func (b *builder) finalize() Snapshot {
	b.addSemanticGoCalls()
	b.resolveReferences()
	s := b.snap
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	sort.Slice(s.Symbols, func(i, j int) bool {
		if s.Symbols[i].Location.Path != s.Symbols[j].Location.Path {
			return s.Symbols[i].Location.Path < s.Symbols[j].Location.Path
		}
		if s.Symbols[i].Location.Line != s.Symbols[j].Location.Line {
			return s.Symbols[i].Location.Line < s.Symbols[j].Location.Line
		}
		return s.Symbols[i].Name < s.Symbols[j].Name
	})
	sort.Slice(s.Edges, func(i, j int) bool {
		if s.Edges[i].From != s.Edges[j].From {
			return s.Edges[i].From < s.Edges[j].From
		}
		if s.Edges[i].To != s.Edges[j].To {
			return s.Edges[i].To < s.Edges[j].To
		}
		if s.Edges[i].Kind != s.Edges[j].Kind {
			return s.Edges[i].Kind < s.Edges[j].Kind
		}
		if s.Edges[i].Symbol != s.Edges[j].Symbol {
			return s.Edges[i].Symbol < s.Edges[j].Symbol
		}
		return s.Edges[i].Location.Line < s.Edges[j].Location.Line
	})
	skipped := sortedUnique(b.skipped)
	if len(skipped) > maxSkippedReported {
		sentinel := fmt.Sprintf("... (%d more skipped)", len(skipped)-maxSkippedReported)
		skipped = append(skipped[:maxSkippedReported:maxSkippedReported], sentinel)
	}
	s.Status = Status{
		Complete:     b.complete,
		Limits:       sortedUnique(b.limits),
		Skipped:      skipped,
		SkippedCount: len(b.skipped),
	}
	diags := sortedDiagnostics(b.diags)
	if len(diags) > maxDiagnosticsKept {
		suppressed := len(diags) - maxDiagnosticsKept
		summary := Diagnostic{Level: "info", Message: fmt.Sprintf("%d additional diagnostics suppressed", suppressed)}
		diags = append(diags[:maxDiagnosticsKept:maxDiagnosticsKept], summary)
	}
	s.Diagnostics = diags
	s.SchemaVersion = SchemaVersion
	return s
}

func (b *builder) resolveReferences() {
	definitions := make(map[string][]string)
	declarations := make(map[sourceReferenceKey]struct{})
	for _, symbol := range b.snap.Symbols {
		definitions[symbol.Name] = append(definitions[symbol.Name], symbol.Location.Path)
		declarations[sourceReferenceKey{Path: symbol.Location.Path, Name: symbol.Name, Line: symbol.Location.Line}] = struct{}{}
	}
	fromPaths := make([]string, 0, len(b.references))
	for from := range b.references {
		fromPaths = append(fromPaths, from)
	}
	slices.Sort(fromPaths)
	for _, from := range fromPaths {
		references := b.references[from]
		for _, reference := range references {
			if _, declaration := declarations[sourceReferenceKey{Path: from, Name: reference.Name, Line: reference.Line}]; declaration {
				continue
			}
			for _, to := range sortedUnique(definitions[reference.Name]) {
				if from == to {
					continue
				}
				b.addEdge(Edge{From: from, To: to, Kind: "references", Symbol: reference.Name, Confidence: ConfidenceSyntactic, Location: Location{Path: from, Line: reference.Line}})
			}
		}
	}
}

type sourceReferenceKey struct {
	Path string
	Name string
	Line int
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	slices.Sort(out)
	return slices.Compact(out)
}

func sortedDiagnostics(diags []Diagnostic) []Diagnostic {
	if len(diags) == 0 {
		return nil
	}
	out := slices.Clone(diags)
	sort.Slice(out, func(i, j int) bool { return diagLess(out[i], out[j]) })
	seen := make(map[string]struct{}, len(out))
	deduped := out[:0]
	for _, d := range out {
		key := diagKey(d)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, d)
	}
	return deduped
}

func diagLess(a, b Diagnostic) bool {
	ap, al, bp, bl := "", 0, "", 0
	if a.Location != nil {
		ap, al = a.Location.Path, a.Location.Line
	}
	if b.Location != nil {
		bp, bl = b.Location.Path, b.Location.Line
	}
	if ap != bp {
		return ap < bp
	}
	if al != bl {
		return al < bl
	}
	if a.Level != b.Level {
		return a.Level < b.Level
	}
	return a.Message < b.Message
}

func diagKey(d Diagnostic) string {
	path, line := "", 0
	if d.Location != nil {
		path, line = d.Location.Path, d.Location.Line
	}
	return fmt.Sprintf("%s|%s|%d|%s", d.Level, path, line, d.Message)
}

func locate(fset *token.FileSet, pos token.Pos, path string) Location {
	p := fset.Position(pos)
	return Location{Path: path, Line: p.Line}
}

// goFuncSymbol extracts one function or method declaration into a Symbol
// with a printed parameter/result signature, receiver, first-sentence doc,
// exportedness, and source span.
func goFuncSymbol(fset *token.FileSet, rel, pkg string, fn *ast.FuncDecl) Symbol {
	kind := "function"
	receiver := ""
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		kind = "method"
		receiver = typeString(fset, fn.Recv.List[0].Type)
	}
	start := locate(fset, fn.Pos(), rel)
	return Symbol{
		Name:      fn.Name.Name,
		Kind:      kind,
		Signature: funcSignature(fset, fn),
		Receiver:  receiver,
		Package:   pkg,
		Exported:  token.IsExported(fn.Name.Name),
		Doc:       firstSentence(fn.Doc),
		EndLine:   fset.Position(fn.End()).Line,
		Location:  start,
	}
}

// goGenSymbols extracts package-level type, constant, and variable
// declarations. Import declarations are handled separately through
// file.Imports and yield no symbols.
func goGenSymbols(fset *token.FileSet, rel, pkg string, decl *ast.GenDecl) []Symbol {
	if decl.Tok == token.TYPE {
		return goTypeSymbols(fset, rel, pkg, decl)
	}
	if decl.Tok == token.CONST {
		return goValueSymbols(fset, rel, pkg, decl, "constant")
	}
	if decl.Tok == token.VAR {
		return goValueSymbols(fset, rel, pkg, decl, "variable")
	}
	return nil
}

func goTypeSymbols(fset *token.FileSet, rel, pkg string, decl *ast.GenDecl) []Symbol {
	var out []Symbol
	for _, spec := range decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		kind, signature := "type", ""
		switch t := ts.Type.(type) {
		case *ast.StructType:
			kind = "struct"
			signature = fieldNames(fset, t.Fields)
		case *ast.InterfaceType:
			kind = "interface"
			signature = fieldNames(fset, t.Methods)
		}
		doc := firstSentence(decl.Doc)
		if ts.Doc != nil {
			doc = firstSentence(ts.Doc)
		}
		out = append(out, Symbol{
			Name:      ts.Name.Name,
			Kind:      kind,
			Signature: signature,
			Package:   pkg,
			Exported:  token.IsExported(ts.Name.Name),
			Doc:       doc,
			EndLine:   fset.Position(ts.End()).Line,
			Location:  locate(fset, ts.Pos(), rel),
		})
	}
	return out
}

func goValueSymbols(fset *token.FileSet, rel, pkg string, decl *ast.GenDecl, kind string) []Symbol {
	var out []Symbol
	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		doc := firstSentence(decl.Doc)
		if vs.Doc != nil {
			doc = firstSentence(vs.Doc)
		}
		for _, name := range vs.Names {
			out = append(out, Symbol{
				Name:     name.Name,
				Kind:     kind,
				Package:  pkg,
				Exported: token.IsExported(name.Name),
				Doc:      doc,
				EndLine:  fset.Position(vs.End()).Line,
				Location: locate(fset, vs.Pos(), rel),
			})
		}
	}
	return out
}

// funcSignature renders "(params) results" without the func keyword.
func funcSignature(fset *token.FileSet, fn *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("(")
	if fn.Type.Params != nil {
		for i, field := range fn.Type.Params.List {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fieldString(fset, field))
		}
	}
	b.WriteString(")")
	results := fn.Type.Results
	if results == nil || len(results.List) == 0 {
		return b.String()
	}
	if len(results.List) == 1 && len(results.List[0].Names) == 0 {
		b.WriteString(" ")
		b.WriteString(typeString(fset, results.List[0].Type))
		return b.String()
	}
	b.WriteString(" (")
	for i, field := range results.List {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fieldString(fset, field))
	}
	b.WriteString(")")
	return b.String()
}

// fieldString renders "name1, name2 Type" or just "Type" for unnamed fields.
func fieldString(fset *token.FileSet, field *ast.Field) string {
	if len(field.Names) == 0 {
		return typeString(fset, field.Type)
	}
	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ", ") + " " + typeString(fset, field.Type)
}

// fieldNames renders struct fields or interface methods as "{A, B}" for the
// compact type signature carried on struct and interface symbols.
func fieldNames(fset *token.FileSet, fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return "{}"
	}
	names := make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			names = append(names, typeString(fset, field.Type))
			continue
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return "{" + strings.Join(names, ", ") + "}"
}

func typeString(fset *token.FileSet, expr ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, expr); err != nil {
		return ""
	}
	return b.String()
}

// firstSentence collapses a doc comment to its first sentence, bounded in
// length so one pathological comment cannot inflate a snapshot.
func firstSentence(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	text := strings.Join(strings.Fields(doc.Text()), " ")
	if text == "" {
		return ""
	}
	if end := strings.IndexByte(text, '.'); end >= 0 {
		text = text[:end+1]
	}
	if runes := []rune(text); len(runes) > maxDocRunes {
		text = string(runes[:maxDocRunes])
	}
	return text
}

// maxDocRunes bounds the doc comment stored per symbol.
const maxDocRunes = 160

func callName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		return n.Sel.Name
	}
	return ""
}

func excluded(path string, names []string) bool {
	for _, name := range names {
		if path == name || strings.HasPrefix(path, name+"/") {
			return true
		}
	}
	return false
}

func language(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return languageGo
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx":
		return "cpp"
	case ".java":
		return "java"
	case ".php":
		return "php"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".rs":
		return "rust"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "jsx"
	default:
		return "unknown"
	}
}

func generated(path string) bool {
	return strings.HasSuffix(path, ".gen.go") || strings.HasSuffix(path, "_generated.go")
}
