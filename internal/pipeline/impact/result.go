package impact

import (
	"fmt"
	"sort"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func (a *analysisState) buildResult() {
	a.affectedStoreMap = copyKeys(a.targetStores)
	a.addDirectPhases()
	a.findDownstreamPhases()
	a.addDownstreamPhases()
	a.sortResult()
	a.setSeverity()
}

func copyKeys(values map[string]bool) map[string]bool {
	copied := make(map[string]bool, len(values))
	for value := range values {
		copied[value] = true
	}
	return copied
}

func (a *analysisState) addDirectPhases() {
	for index := range a.directMap {
		phase := a.contexts[index]
		reads, writes := a.phaseStores(phase)
		for _, store := range writes {
			a.affectedStoreMap[store] = true
		}
		a.addCommand(phase.cmdName)
		a.result.DirectPhases = append(a.result.DirectPhases, affectedPhase(phase, "direct-match", reads, writes))
	}
}

func (a *analysisState) phaseStores(phase phaseCtx) (reads, writes []string) {
	if len(a.symbolFiles) != 0 {
		return nil, nil
	}
	return storesForPhase(phaseStoreLabels(phase.phase.Name, phase.stages), a.spec.Stores)
}

func (a *analysisState) findDownstreamPhases() {
	if len(a.symbolFiles) == 0 {
		a.findSequentialDownstream()
	}
	for index, phase := range a.contexts {
		if a.directMap[index] || a.downstreamMap[index] || !a.phaseReadsAffectedStore(phase) {
			continue
		}
		a.downstreamMap[index] = true
	}
}

func (a *analysisState) findSequentialDownstream() {
	for directIndex := range a.directMap {
		direct := a.contexts[directIndex]
		for index, phase := range a.contexts {
			if !a.directMap[index] && !a.downstreamMap[index] && sequentiallyFollows(a.spec, direct, phase) {
				a.downstreamMap[index] = true
			}
		}
	}
}

func sequentiallyFollows(s *spec.Spec, direct, other phaseCtx) bool {
	if direct.cmdName == "" && other.cmdName == "" && other.ordinal > direct.ordinal {
		return true
	}
	if direct.cmdName == "" && other.cmdName != "" {
		return true
	}
	if direct.cmdName != "" && other.cmdName == direct.cmdName && other.ordinal > direct.ordinal {
		return true
	}
	return s.HasLoop() && direct.cmdName == "" && other.cmdName == "" && direct.phase.Scope == spec.PhaseScopePerIteration && other.phase.Scope == spec.PhaseScopePerIteration
}

func (a *analysisState) phaseReadsAffectedStore(phase phaseCtx) bool {
	if len(a.affectedStoreMap) == 0 {
		return false
	}
	reads, _ := storesForPhase(phaseStoreLabels(phase.phase.Name, phase.stages), a.spec.Stores)
	for _, store := range reads {
		if a.affectedStoreMap[store] {
			return true
		}
	}
	return false
}

func (a *analysisState) addDownstreamPhases() {
	for index := range a.downstreamMap {
		phase := a.contexts[index]
		reads, writes := storesForPhase(phaseStoreLabels(phase.phase.Name, phase.stages), a.spec.Stores)
		for _, store := range writes {
			a.affectedStoreMap[store] = true
		}
		a.addCommand(phase.cmdName)
		a.result.DownstreamPhases = append(a.result.DownstreamPhases, affectedPhase(phase, "downstream-flow", reads, writes))
	}
}

func affectedPhase(phase phaseCtx, reason string, reads, writes []string) AffectedPhase {
	return AffectedPhase{Name: phase.phase.Name, Kind: string(phase.phase.Kind), Command: phase.cmdName, Ordinal: phase.ordinal, Reason: reason, Stages: phase.stages, StoresRead: reads, StoresWrite: writes}
}

func (a *analysisState) addCommand(command string) {
	if command != "" {
		a.affectedCommandMap[command] = true
	}
}

func (a *analysisState) sortResult() {
	sortAffectedPhases(a.result.DirectPhases)
	sortAffectedPhases(a.result.DownstreamPhases)
	a.result.AffectedStores = sortedKeys(a.affectedStoreMap)
	a.result.AffectedCommands = sortedKeys(a.affectedCommandMap)
	a.result.TotalPhases = len(a.result.DirectPhases) + len(a.result.DownstreamPhases)
}

func sortAffectedPhases(phases []AffectedPhase) {
	sort.Slice(phases, func(left, right int) bool {
		if phases[left].Command != phases[right].Command {
			return phases[left].Command < phases[right].Command
		}
		return phases[left].Ordinal < phases[right].Ordinal
	})
}

func (a *analysisState) setSeverity() {
	total := len(a.contexts)
	result := a.result
	switch {
	case result.TotalPhases == 0 && len(result.Matches) == 0:
		result.Severity = "none"
		result.Summary = fmt.Sprintf("Target %q does not intersect any modeled pipeline phases or stores.", a.target)
	case result.TotalPhases == 0:
		result.Severity = "unknown"
		result.Summary = fmt.Sprintf("Target %q exists but is not connected to a modeled phase; blast radius is unknown.", a.target)
	case result.TotalPhases == 1:
		result.Severity = "isolated"
		result.Summary = fmt.Sprintf("Isolated impact: only phase %q is affected.", onlyAffectedPhase(result).Name)
	case total > 0 && float64(result.TotalPhases)/float64(total) >= 0.75:
		result.Severity = "critical"
		result.Summary = fmt.Sprintf("Critical blast radius: changes affect %d of %d phases across %d commands.", result.TotalPhases, total, len(result.AffectedCommands))
	case len(result.AffectedCommands) > 1 || len(result.AffectedStores) > 0:
		result.Severity = "high"
		result.Summary = fmt.Sprintf("High blast radius: changes propagate across %d phases, %d commands, and %d store%s.", result.TotalPhases, len(result.AffectedCommands), len(result.AffectedStores), pluralSuffix(len(result.AffectedStores)))
	default:
		result.Severity = "moderate"
		result.Summary = fmt.Sprintf("Moderate blast radius: affects %d phases in command lane.", result.TotalPhases)
	}
}

func onlyAffectedPhase(result *Result) AffectedPhase {
	if len(result.DirectPhases) > 0 {
		return result.DirectPhases[0]
	}
	return result.DownstreamPhases[0]
}
