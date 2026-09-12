package scan

import (
	"slices"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// Merge combines a freshly scanned spec into an existing human-authored spec.
// Human-authored fields are preserved; new content from scan is added; stale
// content is removed.
func Merge(existing, scanned *spec.Spec) *spec.Spec {
	result := &spec.Spec{
		Title:      existing.Title,      // always keep human title
		Breadcrumb: existing.Breadcrumb, // always keep human breadcrumb
		Loop:       existing.Loop,       // preserve human loop construct
	}
	if result.Loop == nil {
		result.Loop = scanned.Loop
	}

	// Match phases: by name first, then by >50% file overlap.
	matched := map[int]bool{} // indexes into scanned.Phases that got matched
	for _, ep := range existing.Phases {
		si := findMatchingPhase(ep, scanned.Phases)
		if si >= 0 {
			matched[si] = true
			result.Phases = append(result.Phases, mergePhase(ep, scanned.Phases[si]))
		} else {
			// Existing phase not in scan — keep it (human-authored phases may describe
			// non-Go components or conceptual groupings).
			result.Phases = append(result.Phases, ep)
		}
	}
	// Append any new phases from scan that weren't matched.
	for i, sp := range scanned.Phases {
		if !matched[i] {
			result.Phases = append(result.Phases, sp)
		}
	}

	result.Stores = mergeStores(existing.Stores, scanned.Stores)
	result.Commands = mergeCommands(existing.Commands, scanned.Commands)
	result.Coverage = scanned.Coverage
	return result
}

// findMatchingPhase returns the index in scanned that best matches ep,
// or -1 if no match is found.
func findMatchingPhase(ep spec.Phase, scanned []spec.Phase) int {
	// First: exact name match.
	for i, sp := range scanned {
		if sp.Name == ep.Name {
			return i
		}
	}
	// Second: >50% file overlap.
	if len(ep.Files) == 0 {
		return -1
	}
	epFiles := make(map[string]bool, len(ep.Files))
	for _, f := range ep.Files {
		epFiles[f] = true
	}
	for i, sp := range scanned {
		overlap := 0
		for _, f := range sp.Files {
			if epFiles[f] {
				overlap++
			}
		}
		total := max(len(ep.Files), len(sp.Files))
		if total > 0 && overlap*2 > total {
			return i
		}
	}
	return -1
}

// mergePhase combines an existing phase with a scanned phase.
func mergePhase(existing, scanned spec.Phase) spec.Phase {
	result := mergePhaseFields(existing, scanned)
	result.Files = mergePhaseFiles(existing.Files, scanned.Files)
	result.Stages = mergePhaseStages(existing.Stages, scanned.Stages)
	return result
}

// mergePhaseFields starts from the human-authored phase, filling any blank
// identity field from the scanned phase.
func mergePhaseFields(existing, scanned spec.Phase) spec.Phase {
	result := spec.Phase{
		Name:        existing.Name, // keep existing name
		Kind:        existing.Kind,
		Scope:       existing.Scope,
		Description: existing.Description,
		Sources:     existing.Sources,
	}
	if result.Scope == "" {
		result.Scope = scanned.Scope
	}
	if result.Kind == "" {
		result.Kind = scanned.Kind
	}
	if result.Description == "" {
		result.Description = scanned.Description
	}
	return result
}

// mergePhaseFiles unions both file lists, preserving order (existing first).
func mergePhaseFiles(existing, scanned []string) []string {
	var result []string
	seen := make(map[string]bool)
	for _, f := range existing {
		result = append(result, f)
		seen[f] = true
	}
	for _, f := range scanned {
		if !seen[f] {
			result = append(result, f)
			seen[f] = true
		}
	}
	return result
}

// mergePhaseStages keeps ALL existing stages verbatim (external stages are
// human-authored only — scan never emits them, so they are always preserved
// at their original position unchanged), then adds non-duplicating chip and
// fanout stages from the scan.
func mergePhaseStages(existing, scanned []spec.Stage) []spec.Stage {
	result := slices.Clone(existing)
	labels := stageLabels(existing)
	for _, st := range scanned {
		if st.Chip != nil && !labels[st.Chip.Label] {
			result = append(result, st)
			continue
		}
		if st.Fanout != nil {
			if mergeFanoutTargets(result, st.Fanout) {
				continue
			}
			if !labels[st.Fanout.Gate] {
				result = append(result, st)
				labels[st.Fanout.Gate] = true
			}
		}
	}
	return result
}

// stageLabels indexes the dedup keys of every stage in the slice: chip
// labels plus fork, fanout, and external gates/labels.
func stageLabels(stages []spec.Stage) map[string]bool {
	labels := make(map[string]bool)
	for _, st := range stages {
		switch {
		case st.Chip != nil:
			labels[st.Chip.Label] = true
		case st.Fork != nil:
			labels[st.Fork.Gate] = true
		case st.Fanout != nil:
			labels[st.Fanout.Gate] = true
		case st.External != nil:
			labels[st.External.Label] = true
		}
	}
	return labels
}

// mergeFanoutTargets folds scanned fanout targets into the first stage in
// the slice with the same gate, reporting whether such a stage exists.
func mergeFanoutTargets(stages []spec.Stage, scanned *spec.Fanout) bool {
	for i := range stages {
		existing := stages[i].Fanout
		if existing == nil || existing.Gate != scanned.Gate {
			continue
		}
		targets := make(map[string]bool, len(existing.Targets))
		for _, target := range existing.Targets {
			targets[target.Flag+"\x00"+target.Label] = true
		}
		for _, target := range scanned.Targets {
			key := target.Flag + "\x00" + target.Label
			if targets[key] {
				continue
			}
			existing.Targets = append(existing.Targets, target)
			targets[key] = true
		}
		return true
	}
	return false
}

func mergeCommands(existing, scanned []spec.Command) []spec.Command {
	result := make([]spec.Command, 0, len(existing)+len(scanned))
	matched := make(map[int]bool, len(scanned))
	for _, ec := range existing {
		idx := findMatchingCommand(ec.Name, scanned)
		if idx < 0 {
			result = append(result, ec)
			continue
		}
		matched[idx] = true
		sc := scanned[idx]
		merged := spec.Command{
			Name:        ec.Name,
			Description: ec.Description,
			Phases:      mergeCommandPhases(ec.Phases, sc.Phases),
		}
		if merged.Description == "" {
			merged.Description = sc.Description
		}
		result = append(result, merged)
	}
	for i, sc := range scanned {
		if matched[i] {
			continue
		}
		result = append(result, sc)
	}
	return result
}

func findMatchingCommand(name string, commands []spec.Command) int {
	for i, command := range commands {
		if command.Name == name {
			return i
		}
	}
	return -1
}

func mergeCommandPhases(existing, scanned []spec.Phase) []spec.Phase {
	result := make([]spec.Phase, 0, len(existing)+len(scanned))
	matched := map[int]bool{}
	for _, ep := range existing {
		si := findMatchingPhase(ep, scanned)
		if si >= 0 {
			matched[si] = true
			result = append(result, mergePhase(ep, scanned[si]))
		} else {
			result = append(result, ep)
		}
	}
	for i, sp := range scanned {
		if !matched[i] {
			result = append(result, sp)
		}
	}
	return result
}

// mergeStores combines existing and scanned stores.
// Matching by name; human notes preserved.
func mergeStores(existing, scanned []spec.Store) []spec.Store {
	result := make([]spec.Store, len(existing))
	copy(result, existing)

	existingNames := make(map[string]int, len(existing))
	for i, s := range existing {
		existingNames[s.Name] = i
	}

	for _, ss := range scanned {
		if idx, ok := existingNames[ss.Name]; ok {
			mergeStoreWriters(&result[idx], ss)
		} else {
			result = append(result, ss)
		}
	}
	return result
}

// mergeStoreWriters adds newly scanned writer boundaries to an existing
// store, preserving human notes and refreshing source provenance on an
// existing semantic writer.
func mergeStoreWriters(existing *spec.Store, scanned spec.Store) {
	type writerKey struct{ stage, access string }
	existingWriters := make(map[writerKey]int)
	for writerIndex, writer := range existing.Writers {
		existingWriters[writerKey{writer.Stage, writer.Access}] = writerIndex
	}
	for _, writer := range scanned.Writers {
		key := writerKey{writer.Stage, writer.Access}
		if writerIndex, exists := existingWriters[key]; exists {
			target := &existing.Writers[writerIndex]
			target.SourceFile = writer.SourceFile
			target.SourceLine = writer.SourceLine
			target.SourceSymbols = appendUniqueSorted(target.SourceSymbols, writer.SourceSymbols...)
			continue
		}
		existing.Writers = append(existing.Writers, writer)
		existingWriters[key] = len(existing.Writers) - 1
	}
}

func appendUniqueSorted(existing []string, additions ...string) []string {
	seen := make(map[string]bool, len(existing)+len(additions))
	for _, value := range existing {
		seen[value] = true
	}
	for _, value := range additions {
		seen[value] = true
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}
