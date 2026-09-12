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

// fillEmptyPhases populates Stages for any phase that has Files but no Stages.
// This happens when splitLargePhases creates sub-phases from clusters that
// contain no exported symbols — extractStages returns nothing, leaving Stages nil.
func fillEmptyPhases(root string, phases []spec.Phase, maxStages int) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)

	for i, phase := range out {
		if len(phase.Stages) > 0 || len(phase.Files) == 0 {
			continue
		}
		stages, truncated := extractASTStages(root, phase.Files, maxStages)
		out[i].Stages = stages
		out[i].TruncatedCount = truncated
	}

	return out
}

// extractASTStages parses the given relative file paths under root and returns
// chip stages for every non-trivial function body found (bodyLines >= 5).
// Test files are skipped. Results are sorted: exported first (alphabetical),
// then unexported (alphabetical). Capped at maxStages; the overflow count is
// returned as the second value — "+N more" never appears as a chip.
// funcEntry is one candidate chip stage discovered from a function body.
type funcEntry struct {
	label      string
	subtitle   string
	style      spec.ChipStyle
	wide       bool
	exported   bool
	sourceFile string // repo-relative path
	sourceLine int    // 1-based line number of function declaration
}

func extractASTStages(root string, relPaths []string, maxStages int) ([]spec.Stage, int) {
	fset := token.NewFileSet()
	seen := make(map[string]bool)
	var entries []funcEntry
	for _, rel := range relPaths {
		entries = append(entries, fileFuncEntries(fset, root, rel, seen)...)
	}

	slices.SortFunc(entries, compareFuncEntries)

	// Cap at maxStages.
	truncated := 0
	if len(entries) > maxStages {
		truncated = len(entries) - maxStages
		entries = entries[:maxStages]
	}

	stages := make([]spec.Stage, 0, len(entries))
	for _, e := range entries {
		chip := &spec.Chip{
			Label:      e.label,
			Style:      e.style,
			Wide:       e.wide,
			Subtitle:   e.subtitle,
			SourceFile: e.sourceFile,
			SourceLine: e.sourceLine,
		}
		stages = append(stages, spec.Stage{Chip: chip})
	}

	return stages, truncated
}

// fileFuncEntries parses one repo-relative file and returns a chip entry for
// every non-trivial (bodyLines >= 5) function body it declares. Test files
// and unparseable files contribute nothing; the first occurrence of each
// function name wins.
func fileFuncEntries(fset *token.FileSet, root, rel string, seen map[string]bool) []funcEntry {
	if strings.HasSuffix(rel, "_test.go") {
		return nil
	}
	f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.ParseComments)
	if err != nil {
		return nil
	}
	var entries []funcEntry
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if seen[name] {
			continue // keep first occurrence
		}
		if entry, ok := stageEntry(fset, fn, rel); ok {
			seen[name] = true
			entries = append(entries, entry)
		}
	}
	return entries
}

// stageEntry converts one function declaration into a chip entry when its
// body is non-trivial (bodyLines >= 5).
func stageEntry(fset *token.FileSet, fn *ast.FuncDecl, rel string) (funcEntry, bool) {
	startLine := fset.Position(fn.Body.Pos()).Line
	endLine := fset.Position(fn.Body.End()).Line
	bodyLines := endLine - startLine + 1
	if bodyLines < 5 {
		return funcEntry{}, false // trivial
	}
	var subtitle string
	if fn.Doc != nil {
		subtitle = docFirstSentence(fn.Doc.Text(), fn.Name.Name)
	}
	return funcEntry{
		label:      fn.Name.Name,
		subtitle:   subtitle,
		style:      chipStyle(fn.Name.Name),
		wide:       bodyLines > 50,
		exported:   fn.Name.IsExported(),
		sourceFile: rel,
		sourceLine: fset.Position(fn.Pos()).Line,
	}, true
}

// compareFuncEntries orders chip entries exported-first, then alphabetical
// within each group.
func compareFuncEntries(a, b funcEntry) int {
	if a.exported != b.exported {
		if a.exported {
			return -1
		}
		return 1
	}
	return strings.Compare(a.label, b.label)
}
