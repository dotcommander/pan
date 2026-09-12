package scan

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// semanticFSState is the fallback semantic name for unspecified filesystem
// state.
const semanticFSState = "filesystem state"

// pkgFilepath is the standard-library package name for path helpers.
const pkgFilepath = "filepath"

// fnJoin is filepath.Join's function name.
const fnJoin = "Join"

// fileStoreName produces a store name from a path string.
// Returns "" to signal the caller should skip this candidate.
func fileStoreName(path string) string {
	return path
}

// resolvedStorePathName follows file-local assignments, constructor fields,
// helper returns, and filepath.Join calls so stores are named for durable
// locations instead of incidental locals such as path or cachePath.
func resolvedStorePathName(file *ast.File, expr ast.Expr, enclosing string) string {
	resolved := resolveStoreExpr(file, expr, make(map[string]bool))
	contextName := semanticStoreName("", enclosing)
	if cacheSelectorPath(expr, resolved) {
		return "cache: " + resolved
	}
	if resolved == "." && contextName != semanticFSState {
		return contextName + " directory"
	}
	if promptVaultGlob(contextName, resolved) {
		return "prompt vault/*"
	}
	if genericPathSuffix(resolved) {
		return semanticStoreName(resolved, enclosing)
	}
	if resolved == "" || resolved == "path" || resolved == "dir" || resolved == semanticFSState {
		return semanticStoreName(resolved, enclosing)
	}
	return resolved
}

// cacheSelectorPath reports whether expr is a selector whose name mentions
// cache and the resolved path looks like a real location.
func cacheSelectorPath(expr ast.Expr, resolved string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(selector.Sel.Name), "cache") && strings.ContainsAny(resolved, "/.")
}

// promptVaultGlob reports whether a prompt-flavored context resolved to a
// vault-shaped glob.
func promptVaultGlob(contextName, resolved string) bool {
	return strings.Contains(contextName, "prompt") && strings.HasSuffix(resolved, "/*") && !strings.Contains(resolved, "/.")
}

// genericPathSuffix reports whether resolved is a bare identifier-style name
// ending in a path-ish suffix such as Dir, Path, or File.
func genericPathSuffix(resolved string) bool {
	if strings.ContainsAny(resolved, "/.") {
		return false
	}
	return strings.HasSuffix(resolved, "Dir") || strings.HasSuffix(resolved, "Path") || strings.HasSuffix(resolved, "File")
}

// resolveStoreExpr resolves one expression to a store path string by
// dispatching on its AST shape.
func resolveStoreExpr(file *ast.File, expr ast.Expr, seen map[string]bool) string {
	switch value := expr.(type) {
	case *ast.BasicLit:
		return storePathName(value)
	case *ast.Ident:
		return resolveStoreIdent(file, value, seen)
	case *ast.SelectorExpr:
		return resolveStoreField(file, value, seen)
	case *ast.CallExpr:
		return resolveStoreCall(file, value, seen)
	default:
		return ""
	}
}

// bestStoreCandidate picks the most specific candidate path, reporting
// whether any candidate existed.
func bestStoreCandidate(candidates []string) (string, bool) {
	if len(candidates) == 0 {
		return "", false
	}
	sort.Slice(candidates, func(i, j int) bool {
		return storeSpecificity(candidates[i]) > storeSpecificity(candidates[j])
	})
	return candidates[0], true
}

// resolveStoreIdent resolves an identifier: `home` to "~", otherwise the
// most specific value assigned to that identifier anywhere in the file, or
// the bare name.
func resolveStoreIdent(file *ast.File, value *ast.Ident, seen map[string]bool) string {
	if value.Name == "home" {
		return "~"
	}
	key := "ident:" + value.Name
	if seen[key] {
		return semanticStoreName(value.Name, "")
	}
	seen[key] = true
	defer delete(seen, key)
	var candidates []string
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for index, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if ok && ident.Name == value.Name && index < len(assign.Rhs) {
				if candidate := resolveStoreExpr(file, assign.Rhs[index], seen); candidate != "" {
					candidates = append(candidates, candidate)
				}
			}
		}
		return true
	})
	if best, ok := bestStoreCandidate(candidates); ok {
		return best
	}
	return value.Name
}

// resolveStoreField resolves a selector expression (x.Field) to the most
// specific value assigned to that field name in a composite literal
// anywhere in the file, or the field's semantic name.
func resolveStoreField(file *ast.File, value *ast.SelectorExpr, seen map[string]bool) string {
	key := "field:" + value.Sel.Name
	if seen[key] {
		return semanticStoreName(value.Sel.Name, "")
	}
	seen[key] = true
	defer delete(seen, key)
	var candidates []string
	ast.Inspect(file, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		ident, ok := pair.Key.(*ast.Ident)
		if ok && ident.Name == value.Sel.Name {
			if candidate := resolveStoreExpr(file, pair.Value, seen); candidate != "" {
				candidates = append(candidates, candidate)
			}
		}
		return true
	})
	if best, ok := bestStoreCandidate(candidates); ok {
		return best
	}
	return semanticStoreName(value.Sel.Name, "")
}

// resolveStoreCall resolves a call expression: filepath.Dir and
// filepath.Join of resolvable parts, x.Name() to "*", or a local helper
// function's most specific returned path.
func resolveStoreCall(file *ast.File, value *ast.CallExpr, seen map[string]bool) string {
	if resolved, ok := resolveStorePkgCall(file, value, seen); ok {
		return resolved
	}
	if ident, ok := value.Fun.(*ast.Ident); ok {
		return resolveStoreFuncCall(file, ident, seen)
	}
	return ""
}

// resolveStorePkgCall handles package-qualified calls: filepath.Dir(arg)
// yields the parent directory, filepath.Join(args...) joins resolved parts
// ("*" replacing unresolvable ones), and x.Name() yields "*". ok is false
// when the call is none of these.
func resolveStorePkgCall(file *ast.File, value *ast.CallExpr, seen map[string]bool) (string, bool) {
	selector, ok := value.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	if pkg.Name == pkgFilepath && selector.Sel.Name == "Dir" && len(value.Args) == 1 {
		resolved := resolveStoreExpr(file, value.Args[0], seen)
		if resolved != "" {
			return filepath.ToSlash(filepath.Dir(resolved)), true
		}
	}
	if pkg.Name == pkgFilepath && selector.Sel.Name == fnJoin {
		return filepath.ToSlash(filepath.Join(joinParts(file, value.Args, seen)...)), true
	}
	if selector.Sel.Name == "Name" {
		return "*", true
	}
	return "", false
}

// joinParts resolves every Join argument, substituting "*" for empty
// resolutions.
func joinParts(file *ast.File, args []ast.Expr, seen map[string]bool) []string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		part := resolveStoreExpr(file, arg, seen)
		if part == "" {
			part = "*"
		}
		parts = append(parts, part)
	}
	return parts
}

// resolveStoreFuncCall resolves a call to a file-local function by name to
// the most specific path that function returns, or the function's semantic
// name.
func resolveStoreFuncCall(file *ast.File, ident *ast.Ident, seen map[string]bool) string {
	key := "func:" + ident.Name
	if seen[key] {
		return semanticStoreName(ident.Name, "")
	}
	seen[key] = true
	defer delete(seen, key)
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Name.Name != ident.Name || function.Body == nil {
			continue
		}
		if best, ok := returnedStoreCandidate(file, function, seen); ok {
			return best
		}
	}
	return semanticStoreName(ident.Name, "")
}

// returnedStoreCandidate collects the function's first return expressions
// and picks the most specific resolved path.
func returnedStoreCandidate(file *ast.File, function *ast.FuncDecl, seen map[string]bool) (string, bool) {
	var candidates []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		ret, ok := node.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			return true
		}
		if candidate := resolveStoreExpr(file, ret.Results[0], seen); candidate != "" {
			candidates = append(candidates, candidate)
		}
		return true
	})
	return bestStoreCandidate(candidates)
}

func storeSpecificity(value string) int {
	score := len(value)
	if strings.ContainsAny(value, "/.") {
		score += 100
	}
	if strings.Contains(value, "*") {
		score -= 10
	}
	return score
}

func semanticStoreName(name, enclosing string) string {
	words := splitStoreWords(name)
	lower := strings.ToLower(strings.Join(words, " "))
	switch {
	case strings.Contains(lower, "output"):
		return "output file"
	case strings.Contains(lower, "prompt") && strings.Contains(lower, "file"):
		return "prompt vault files"
	case strings.Contains(lower, "vault"), strings.Contains(lower, "prompt") && strings.Contains(lower, "dir"):
		return "prompt vault"
	case strings.Contains(lower, "cache"):
		return "cache file"
	case strings.Contains(lower, "config"):
		return "config file"
	case lower != "" && lower != "path" && lower != "dir":
		return lower
	}
	return enclosingSemanticName(enclosing)
}

// enclosingSemanticName derives a semantic store name from the enclosing
// function's name after stripping common state verbs.
func enclosingSemanticName(enclosing string) string {
	context := strings.ToLower(strings.Join(splitStoreWords(enclosing), " "))
	for _, verb := range []string{"load ", "save ", "read ", "write ", "scan ", "ensure ", "inspect ", "run "} {
		context = strings.ReplaceAll(context, verb, "")
	}
	context = strings.TrimSpace(context)
	switch {
	case strings.Contains(context, "prompt"):
		return "prompt vault"
	case context != "":
		return context
	default:
		return semanticFSState
	}
}

func splitStoreWords(value string) []string {
	value = strings.TrimSuffix(value, "()")
	var words []string
	start := 0
	for index, r := range value {
		if index > start && (r == '_' || r == '-' || unicode.IsUpper(r)) {
			words = append(words, strings.TrimSpace(value[start:index]))
			if r == '_' || r == '-' {
				start = index + 1
			} else {
				start = index
			}
		}
	}
	if start < len(value) {
		words = append(words, strings.TrimSpace(value[start:]))
	}
	return words
}

// storePathName extracts a store path from a path expression.
// Returns "" to signal the caller should skip this candidate.
// Only string literals and filepath.Join of all-literal args are accepted;
// variable names, field accesses, and map lookups are not filesystem paths.
func storePathName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return stringLiteralValue(e)
	case *ast.Ident:
		return identStorePathName(e)
	case *ast.SelectorExpr:
		if owner, ok := e.X.(*ast.Ident); ok {
			return owner.Name + "." + e.Sel.Name
		}
		return e.Sel.Name
	case *ast.IndexExpr:
		// Map/slice accesses (e.g., cache[key]) are not paths — drop.
		return ""
	case *ast.CallExpr:
		return storeCallName(e)
	default:
		return ""
	}
}

// stringLiteralValue unquotes one string literal, or returns "" for other
// literal kinds.
func stringLiteralValue(lit *ast.BasicLit) string {
	if lit.Kind != token.STRING {
		return ""
	}
	s := lit.Value
	if len(s) >= 2 && (s[0] == '"' || s[0] == '`') {
		s = s[1 : len(s)-1]
	}
	return s
}

// identStorePathName resolves an identifier used as a path: package-local
// consts to their literal value, assignments and var declarations to their
// dynamic name, and anything else to the bare identifier.
func identStorePathName(e *ast.Ident) string {
	if e.Obj == nil {
		return e.Name
	}
	if path := constStorePathName(e); path != "" {
		return path
	}
	if path := assignedStorePathName(e); path != "" {
		return path
	}
	return e.Name
}

// constStorePathName resolves an identifier bound to a single string-literal
// const declaration.
func constStorePathName(e *ast.Ident) string {
	if e.Obj.Kind != ast.Con {
		return ""
	}
	spec, ok := e.Obj.Decl.(*ast.ValueSpec)
	if !ok || len(spec.Values) != 1 {
		return ""
	}
	if lit, ok := spec.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return stringLiteralValue(lit)
	}
	return ""
}

// assignedStorePathName resolves an identifier bound by an assignment or a
// var declaration to the dynamic name of its value expression.
func assignedStorePathName(e *ast.Ident) string {
	switch decl := e.Obj.Decl.(type) {
	case *ast.AssignStmt:
		return assignedFromAssign(decl, e.Name)
	case *ast.ValueSpec:
		return assignedFromValueSpec(decl, e.Name)
	}
	return ""
}

// assignedFromAssign resolves the value expression bound to name on the left
// side of one assignment.
func assignedFromAssign(decl *ast.AssignStmt, name string) string {
	for i, lhs := range decl.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if ok && ident.Name == name && i < len(decl.Rhs) {
			if resolved := dynamicStorePathName(decl.Rhs[i]); resolved != "" {
				return resolved
			}
		}
	}
	return ""
}

// assignedFromValueSpec resolves the value expression bound to name in one
// var declaration.
func assignedFromValueSpec(decl *ast.ValueSpec, name string) string {
	for i, declared := range decl.Names {
		if declared.Name == name && i < len(decl.Values) {
			if resolved := dynamicStorePathName(decl.Values[i]); resolved != "" {
				return resolved
			}
		}
	}
	return ""
}

func dynamicStorePathName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if ident, ok := e.Fun.(*ast.Ident); ok {
			return ident.Name + "()"
		}
		if name := storeCallName(e); name != "" {
			return name
		}
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.BasicLit:
		return storePathName(e)
	}
	return ""
}

func storeSourceSymbols(expr ast.Expr) []string {
	if expr == nil {
		return nil
	}
	symbols := make(map[string]bool)
	var walk func(ast.Expr)
	walk = func(value ast.Expr) {
		if value == nil {
			return
		}
		switch node := value.(type) {
		case *ast.Ident:
			symbols[node.Name] = true
		case *ast.SelectorExpr:
			symbols[node.Sel.Name] = true
		case *ast.CallExpr:
			for _, arg := range node.Args {
				walk(arg)
			}
		case *ast.ParenExpr:
			walk(node.X)
		case *ast.UnaryExpr:
			walk(node.X)
		case *ast.BinaryExpr:
			walk(node.X)
			walk(node.Y)
		case *ast.IndexExpr:
			walk(node.X)
			walk(node.Index)
		case *ast.SliceExpr:
			walk(node.X)
			walk(node.Low)
			walk(node.High)
			walk(node.Max)
		}
	}
	walk(expr)
	result := make([]string, 0, len(symbols))
	for symbol := range symbols {
		result = append(result, symbol)
	}
	sort.Strings(result)
	return result
}

// storeCallName extracts a store name from a call expression used as a path.
// Handles filepath.Join(...) and os.CreateTemp(dir, pattern).
// Returns "" to signal the caller should skip this candidate.
func storeCallName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	switch {
	case pkg.Name == pkgFilepath && sel.Sel.Name == fnJoin:
		return joinLiteralPath(call.Args)
	case pkg.Name == "os" && sel.Sel.Name == "CreateTemp":
		return createTempName(call.Args)
	default:
		return ""
	}
}

// joinLiteralPath joins a filepath.Join of all-literal string arguments;
// any variable contaminates the path and makes it non-literal — drop rather
// than emit placeholder parts.
func joinLiteralPath(args []ast.Expr) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return ""
		}
		parts = append(parts, stringLiteralValue(lit))
	}
	if len(parts) > 0 {
		return strings.Join(parts, "/")
	}
	return ""
}

// createTempName derives the store name from os.CreateTemp's pattern
// argument, defaulting to "temp file".
func createTempName(args []ast.Expr) string {
	if len(args) >= 2 {
		if lit, ok := args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return stringLiteralValue(lit)
		}
	}
	return "temp file"
}

// openFileAccess derives an access string from the flags argument of os.OpenFile.
func openFileAccess(flagsExpr ast.Expr) string {
	// Walk the expression looking for O_WRONLY, O_RDWR, O_APPEND.
	write := false
	read := false
	ast.Inspect(flagsExpr, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		switch ident.Name {
		case "O_WRONLY", "O_APPEND", "O_CREATE":
			write = true
		case "O_RDWR":
			read = true
			write = true
		case "O_RDONLY":
			read = true
		}
		return true
	})
	switch {
	case read && write:
		return accessReadWrite
	case write:
		return "w"
	default:
		return "r"
	}
}

// openFileNote derives a human-readable note from an os.OpenFile access string
// and the flags expression.
func openFileNote(access string, flagsExpr ast.Expr) string {
	// Check for O_APPEND in flags.
	hasAppend := false
	ast.Inspect(flagsExpr, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == "O_APPEND" {
			hasAppend = true
		}
		return !hasAppend
	})
	if hasAppend {
		return "append"
	}
	switch access {
	case "r":
		return "read"
	case "w":
		return "write"
	case accessReadWrite:
		return "read/write"
	default:
		return ""
	}
}
