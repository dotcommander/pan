package scan

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// mergePhases reduces the phase list to at most maxPhases by merging the
// smallest adjacent phase pair repeatedly.
func mergePhases(phases []spec.Phase, maxPhases int) []spec.Phase {
	for len(phases) > maxPhases {
		// Find the adjacent pair with the combined minimum file count.
		minIdx := 0
		minCount := len(phases[0].Files) + len(phases[1].Files)
		for i := 1; i < len(phases)-1; i++ {
			count := len(phases[i].Files) + len(phases[i+1].Files)
			if count < minCount {
				minCount = count
				minIdx = i
			}
		}
		// Merge minIdx+1 into minIdx.
		a := phases[minIdx]
		b := phases[minIdx+1]
		merged := spec.Phase{
			Name:   mergeName(a, b),
			Kind:   a.Kind,
			Files:  append(a.Files, b.Files...),
			Stages: append(a.Stages, b.Stages...),
		}
		phases = append(phases[:minIdx], append([]spec.Phase{merged}, phases[minIdx+2:]...)...)
	}
	return phases
}

// mergeName picks a good name for a merged phase.
//
// Rules (applied in order):
//  1. If either phase has Kind "Boot", keep its name (boot phases are usually correctly named).
//  2. If either phase has Kind "Emit", keep its name (emit phases are usually correctly named).
//  3. If both phases share the same Kind, keep the first phase's name.
//  4. If kinds differ, use the first phase's Kind capitalized as the name.
func mergeName(a, b spec.Phase) string {
	// Boot and Serve/Render phases are typically already well-named — prefer them.
	if a.Kind == spec.PhaseKindBoot {
		return a.Name
	}
	if b.Kind == spec.PhaseKindBoot {
		return b.Name
	}
	if a.Kind == spec.PhaseKindServe || a.Kind == spec.PhaseKindRender {
		return a.Name
	}
	if b.Kind == spec.PhaseKindServe || b.Kind == spec.PhaseKindRender {
		return b.Name
	}
	// Same kind → keep first phase's name.
	if a.Kind != "" && a.Kind == b.Kind {
		return a.Name
	}
	// Different kinds → use the first phase's kind as the name.
	if a.Kind != "" {
		return string(a.Kind)
	}
	return a.Name
}

// splitLargePhases breaks large single-package phases into sub-phases based on
// filename stem clustering. Files sharing a prefix (e.g., tool_read.go,
// tool_write.go, tool_shell.go) cluster together. Each cluster with exported
// symbols becomes its own phase.
func splitLargePhases(phases []spec.Phase, ranked []symbols.RankedFile, root string, cfg Config) []spec.Phase {
	// Build a lookup from relative path → RankedFile for re-extraction.
	rankedByPath := make(map[string]symbols.RankedFile, len(ranked))
	for _, rf := range ranked {
		rankedByPath[relPath(root, rf.Path)] = rf
	}

	var out []spec.Phase
	for _, phase := range phases {
		if !splittablePhase(phase, len(out), cfg) {
			out = append(out, phase)
			continue
		}
		clusters := clusterByFileStem(phase.Files)
		if len(clusters) < 2 {
			out = append(out, phase)
			continue
		}
		for _, cl := range clusters {
			out = append(out, clusterPhase(cl, phase, rankedByPath, cfg))
		}
	}
	return out
}

// splittablePhase reports whether a phase should be split: it has enough
// files, room remains under MaxPhases, it is not a command-directory Route
// phase (cobra files form one logical dispatch phase), and all its files
// share one package prefix.
func splittablePhase(phase spec.Phase, outLen int, cfg Config) bool {
	if len(phase.Files) < 4 || outLen+1 >= cfg.MaxPhases {
		return false
	}
	if phase.Kind == spec.PhaseKindRoute && commandDirectoryPhase(phase.Files) {
		return false
	}
	return singlePackage(phase.Files)
}

// commandDirectoryPhase reports whether every file lives in a command-style
// directory (commands/, command/, or cmd/).
func commandDirectoryPhase(files []string) bool {
	for _, f := range files {
		dir := filepath.Dir(f)
		if !strings.Contains(dir, "commands") && !strings.Contains(dir, commandWord) && !strings.Contains(dir, "cmd") {
			return false
		}
	}
	return true
}

// singlePackage reports whether every file shares the same two-component
// package prefix.
func singlePackage(files []string) bool {
	prefix := groupKey(files[0])
	for _, f := range files[1:] {
		if groupKey(f) != prefix {
			return false
		}
	}
	return true
}

// clusterPhase builds one sub-phase from a file cluster, inheriting the
// parent phase's kind and description when the cluster's own kind is
// unknown.
func clusterPhase(cl cluster, parent spec.Phase, rankedByPath map[string]symbols.RankedFile, cfg Config) spec.Phase {
	name := capitalize(cl.name)
	if name == "" {
		name = parent.Name
	}
	kind := inferKind(cl.name, nil)
	if kind == "" {
		kind = parent.Kind // inherit parent kind
	}
	desc := kindDescription(kind)
	if desc == "" {
		desc = parent.Description
	}

	clRanked := make([]symbols.RankedFile, 0, len(cl.files))
	for _, f := range cl.files {
		if rf, ok := rankedByPath[f]; ok {
			clRanked = append(clRanked, rf)
		}
	}
	stages, truncated := extractStages(clRanked, cfg.MaxStages, kind)
	return spec.Phase{
		Name:           name,
		Kind:           kind,
		Description:    desc,
		Files:          cl.files,
		Stages:         stages,
		TruncatedCount: truncated,
	}
}

// cluster groups files by their stem prefix for phase splitting.
type cluster struct {
	name  string   // stem name (e.g., "tool", "security")
	files []string // relative file paths
}

// clusterByFileStem groups files by their base name stem.
// Files sharing an underscore-delimited prefix cluster together:
//   - tool_read.go, tool_write.go, tool_shell.go → "tool" cluster
//   - security.go → "security" cluster
//   - engine.go → "engine" cluster
//
// Single-file clusters are merged with the largest cluster to avoid
// one-file phases that add noise.
func clusterByFileStem(files []string) []cluster {
	// Extract stems and group.
	groups := make(map[string][]string) // stem → files
	var order []string

	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".go")
		stem := base
		// Use prefix before first underscore if present.
		if idx := strings.IndexByte(base, '_'); idx > 0 {
			stem = base[:idx]
		}
		if _, exists := groups[stem]; !exists {
			order = append(order, stem)
		}
		groups[stem] = append(groups[stem], f)
	}

	// Build clusters.
	clusters := make([]cluster, 0, len(order))
	for _, stem := range order {
		sort.Strings(groups[stem])
		clusters = append(clusters, cluster{name: stem, files: groups[stem]})
	}

	return clusters
}
