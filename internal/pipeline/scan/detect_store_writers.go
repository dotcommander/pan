package scan

import (
	"go/ast"
	"go/token"
	"slices"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// findEnclosingFunc returns the name of the top-level FuncDecl containing pos.
// Returns "" if no match.
func findEnclosingFunc(file *ast.File, pos token.Pos) string {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Body.Pos() <= pos && pos <= fn.Body.End() {
			return fn.Name.Name
		}
	}
	return ""
}

// mapToStageLabel returns the enclosing function when it is represented by a
// chip; otherwise it attributes the boundary to its containing phase. Store
// references must point at labels that actually exist in the emitted spec.
func mapToStageLabel(funcName string, labels map[string]bool, phaseName string) string {
	if labels[funcName] {
		return funcName
	}
	return phaseName
}

// chipLabels builds a set of all chip stage labels across all phases.
func chipLabels(phases []spec.Phase) map[string]bool {
	m := make(map[string]bool)
	for _, p := range phases {
		for _, s := range p.Stages {
			if s.Chip != nil {
				m[s.Chip.Label] = true
			}
		}
	}
	return m
}

// flattenStores converts the storeMap to a deduplicated sorted slice.
func flattenStores(storeMap map[string][]spec.Writer) []spec.Store {
	if len(storeMap) == 0 {
		return nil
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(storeMap))
	for k := range storeMap {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	stores := make([]spec.Store, 0, len(keys))
	for _, name := range keys {
		writers := dedupWriters(storeMap[name])
		stores = append(stores, spec.Store{Name: name, Writers: writers})
	}
	return stores
}

// dedupWriters removes duplicate (Stage, Access) pairs from a writer list.
func dedupWriters(writers []spec.Writer) []spec.Writer {
	type key struct{ stage, access string }
	seen := make(map[key]bool)
	var out []spec.Writer
	for _, w := range writers {
		k := key{w.Stage, w.Access}
		if !seen[k] {
			seen[k] = true
			out = append(out, w)
		}
	}
	return out
}
