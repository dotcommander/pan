package scan

import (
	"go/ast"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// Disambiguated phase names derived from directory semantics.
const (
	phaseNameDispatch = "Dispatch"
	phaseNameRender   = "Render"
	phaseNameSeed     = "Seed"
)

// orderByCallChain reorders chip stages within each phase to follow the
// intra-phase call graph: callers before callees. Falls back to the existing
// importance-based order for functions not connected in the call graph.
func orderByCallChain(root string, phases []spec.Phase) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)

	for pi, phase := range out {
		files, _ := parseGoFiles(root, phase.Files)
		if len(files) == 0 {
			continue
		}
		chipSet := chipLabelIndexes(phase.Stages)
		if len(chipSet) < 2 {
			continue
		}
		callees, calledBy := buildChipCallGraph(files, chipSet)
		// If no call relationships found, keep existing order.
		if len(callees) == 0 {
			continue
		}
		sorted := topoOrderChips(chipSet, callees, calledBy)
		out[pi].Stages = applyChipOrder(out[pi].Stages, sorted)
	}

	return out
}

// buildChipCallGraph returns caller→callees and the set of called chips for
// every function in files whose name is a chip label in this phase.
func buildChipCallGraph(files []*ast.File, chipSet map[string]int) (map[string]map[string]bool, map[string]bool) {
	callees := make(map[string]map[string]bool) // caller → {callee}
	calledBy := make(map[string]bool)           // functions called by others
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			callerName := fn.Name.Name
			if _, inPhase := chipSet[callerName]; !inPhase {
				continue
			}
			collectChipCallees(fn.Body, callerName, chipSet, callees, calledBy)
		}
	}
	return callees, calledBy
}

// collectChipCallees records the in-phase call edges made by one function body.
func collectChipCallees(body *ast.BlockStmt, callerName string, chipSet map[string]int, callees map[string]map[string]bool, calledBy map[string]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var calleeName string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			calleeName = fn.Name
		case *ast.SelectorExpr:
			calleeName = fn.Sel.Name
		}
		if _, inPhase := chipSet[calleeName]; inPhase && calleeName != callerName {
			if callees[callerName] == nil {
				callees[callerName] = make(map[string]bool)
			}
			callees[callerName][calleeName] = true
			calledBy[calleeName] = true
		}
		return true
	})
}

// topoOrderChips orders chip labels callers-first via BFS from the roots
// (chips not called by any other chip), with disconnected chips appended.
// Ties keep the original stage order.
func topoOrderChips(chipSet map[string]int, callees map[string]map[string]bool, calledBy map[string]bool) []string {
	byIndex := func(a, b string) int { return chipSet[a] - chipSet[b] }

	// Start with roots (chips not called by any other chip in this phase),
	// sorted by their original index for stability.
	var roots []string
	for label := range chipSet {
		if !calledBy[label] {
			roots = append(roots, label)
		}
	}
	slices.SortFunc(roots, byIndex)

	var sorted []string
	visited := make(map[string]bool)
	queue := roots
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true
		sorted = append(sorted, name)

		// Add callees in original order.
		if cs, ok := callees[name]; ok {
			var next []string
			for c := range cs {
				if !visited[c] {
					next = append(next, c)
				}
			}
			slices.SortFunc(next, byIndex)
			queue = append(queue, next...)
		}
	}

	// Add any chips not reached by the call graph (disconnected).
	for label := range chipSet {
		if !visited[label] {
			sorted = append(sorted, label)
		}
	}
	return sorted
}

// applyChipOrder rewrites the chip stage slots with the stages named in
// sorted order. Non-chip stages (forks, fanouts) stay in place.
func applyChipOrder(stages []spec.Stage, sorted []string) []spec.Stage {
	chipStages := make(map[string]spec.Stage)
	var chipIndices []int
	for si, s := range stages {
		if s.Chip != nil {
			chipStages[s.Chip.Label] = s
			chipIndices = append(chipIndices, si)
		}
	}
	for i, label := range sorted {
		if i < len(chipIndices) {
			if s, ok := chipStages[label]; ok {
				stages[chipIndices[i]] = s
			}
		}
	}
	return stages
}

// capitalize returns s with the first rune uppercased.
func capitalize(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// deduplicatePhaseNames resolves duplicate phase names by deriving
// disambiguated names from file paths and kind information.
func deduplicatePhaseNames(phases []spec.Phase) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)

	// Count name occurrences.
	nameCount := make(map[string]int)
	for _, p := range out {
		if p.Name != "" {
			nameCount[p.Name]++
		}
	}

	for i := range out {
		if nameCount[out[i].Name] <= 1 {
			continue
		}
		disambiguatePhaseName(&out[i], phasesWithout(out, i))
	}

	return out
}

// disambiguatePhaseName renames one duplicated phase in place, first from its
// file paths and then — if the derived name still collides — from its kind.
func disambiguatePhaseName(phase *spec.Phase, others []spec.Phase) {
	// Try to derive a better name from file paths.
	if len(phase.Files) > 0 {
		dir := filepath.Dir(phase.Files[0])
		switch {
		case strings.Contains(dir, "commands") || strings.Contains(dir, commandWord):
			phase.Name = phaseNameDispatch
		case strings.Contains(dir, "render"):
			phase.Name = phaseNameRender
		case strings.Contains(dir, "seed"):
			phase.Name = phaseNameSeed
		default:
			phase.Name = capitalize(filepath.Base(dir))
		}
	}

	// If still duplicate, append kind as disambiguator.
	for _, other := range others {
		if other.Name == phase.Name && phase.Kind != "" {
			phase.Name = string(phase.Kind)
			return
		}
	}
}

// phasesWithout returns a shallow copy of phases excluding index i.
func phasesWithout(phases []spec.Phase, i int) []spec.Phase {
	out := make([]spec.Phase, 0, len(phases)-1)
	out = append(out, phases[:i]...)
	out = append(out, phases[i+1:]...)
	return out
}

// relPath returns the path of abs relative to root.
// Falls back to abs on error (e.g. cross-device paths).
func relPath(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return rel
}
