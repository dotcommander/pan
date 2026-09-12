package scan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// mainIdent is the shared spelling of Go's entrypoint identifiers: the
// `main` package name and the `func main()` declaration.
const mainIdent = "main"

// findMainEntrypoint locates the program entrypoint — the `package main` file
// declaring `func main()` — and returns a breadcrumb waypoint for it: the
// directory (e.g. "cmd/Pan") or, for a root-level entrypoint whose
// directory is ".", the bare filename (e.g. "main.go"). When several main
// packages exist (multi-binary repos) the shallowest path wins, ties broken
// alphabetically, so a root main.go beats a nested cmd/<x>/main.go. Returns ""
// when no entrypoint is found, letting the caller fall back to ranking order.
func findMainEntrypoint(absRoot string) string {
	fset := token.NewFileSet()
	var candidates []string
	_ = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err == nil {
			return visitEntrypointCandidate(fset, absRoot, path, d, &candidates)
		}
		return nil
	})
	return pickEntrypoint(candidates)
}

// visitEntrypointCandidate records path as an entrypoint candidate when it is
// a non-test Go file in package main declaring a top-level func main;
// directories that are never analyzed prune the walk.
func visitEntrypointCandidate(fset *token.FileSet, absRoot, path string, d fs.DirEntry, candidates *[]string) error {
	if d.IsDir() {
		switch d.Name() {
		case "testdata", "vendor", "node_modules":
			return filepath.SkipDir
		}
		if path != absRoot && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		return nil
	}
	if isMainPackageFile(fset, path) {
		*candidates = append(*candidates, relPath(absRoot, path))
	}
	return nil
}

// isMainPackageFile reports whether path is a non-test Go file whose package
// clause is main and that declares a top-level func main().
func isMainPackageFile(fset *token.FileSet, path string) bool {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	pf, perr := parser.ParseFile(fset, path, nil, parser.PackageClauseOnly)
	if perr != nil || pf.Name == nil || pf.Name.Name != mainIdent {
		return false
	}
	ff, ferr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if ferr != nil {
		return false
	}
	return declaresFuncMain(ff)
}

// declaresFuncMain reports whether the file declares a receiverless
// top-level func main().
func declaresFuncMain(file *ast.File) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == mainIdent {
			return true
		}
	}
	return false
}

// pickEntrypoint returns the breadcrumb waypoint for the shallowest
// candidate (ties broken alphabetically): its directory, or the bare
// filename for a root-level entrypoint. Empty when no candidates exist.
func pickEntrypoint(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		ci := strings.Count(candidates[i], string(filepath.Separator))
		cj := strings.Count(candidates[j], string(filepath.Separator))
		if ci != cj {
			return ci < cj
		}
		return candidates[i] < candidates[j]
	})
	if dir := filepath.Dir(candidates[0]); dir != "." {
		return dir
	}
	return filepath.Base(candidates[0])
}

// buildBreadcrumb builds a clean phase-name breadcrumb. For the first (Boot)
// phase it uses the real func main() location (e.g. "cmd/Pan" or, for a
// root entrypoint, "main.go"), falling back to the top-ranked Boot file's
// directory. All other phases use the phase Name directly.
func buildBreadcrumb(absRoot string, phases []spec.Phase) string {
	if len(phases) == 0 {
		return ""
	}
	waypoints := make([]string, 0, len(phases))
	waypoints = append(waypoints, bootWaypoint(absRoot, phases[0]))
	for _, phase := range phases[1:] {
		waypoints = append(waypoints, phase.Name)
	}
	return strings.Join(dedupeAdjacent(waypoints), " → ")
}

// bootWaypoint names the first waypoint: the real func main() location, or
// when absent, the top-ranked Boot file's directory prefix, or finally the
// phase name itself.
func bootWaypoint(absRoot string, boot spec.Phase) string {
	if entry := findMainEntrypoint(absRoot); entry != "" {
		return entry
	}
	if len(boot.Files) > 0 {
		if dir := filepath.Dir(boot.Files[0]); dir != "." {
			return dir
		}
	}
	return boot.Name
}

// dedupeAdjacent removes immediately repeated waypoints.
func dedupeAdjacent(waypoints []string) []string {
	var deduped []string
	for _, w := range waypoints {
		if len(deduped) == 0 || deduped[len(deduped)-1] != w {
			deduped = append(deduped, w)
		}
	}
	return deduped
}
