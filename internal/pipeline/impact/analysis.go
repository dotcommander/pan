package impact

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

type phaseCtx struct {
	phase    spec.Phase
	cmdName  string
	ordinal  int
	stages   []string
	allFiles []string
}

type analysisState struct {
	spec                *spec.Spec
	root                string
	target              string
	normTarget          string
	ranked              []symbols.RankedFile
	result              *Result
	contexts            []phaseCtx
	modeledFiles        map[string]bool
	storeWrites         map[string][]string
	symbolFiles         []string
	symbolCommands      []string
	commandScopedSymbol bool
	preserveOwnerPhase  bool
	targetStores        map[string]bool
	directMap           map[int]bool
	downstreamMap       map[int]bool
	affectedStoreMap    map[string]bool
	affectedCommandMap  map[string]bool
}

// Analyze evaluates the blast radius of target against spec s and root.
// ranked may optionally provide symbol index information.
func Analyze(_ context.Context, s *spec.Spec, root, target string, ranked []symbols.RankedFile) (*Result, error) {
	if s == nil {
		return nil, errors.New("nil spec")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("empty target")
	}

	state := newAnalysisState(s, root, target, ranked)
	state.resolveTargets()
	state.findDirectPhases()
	state.findCommandScopedPhases()
	state.findStoreWriterPhases()
	state.addUnmodeledMatches()
	state.buildResult()
	return state.result, nil
}

func newAnalysisState(s *spec.Spec, root, target string, ranked []symbols.RankedFile) *analysisState {
	contexts, modeledFiles := phaseContexts(s, root)
	return &analysisState{
		spec:               s,
		root:               root,
		target:             target,
		normTarget:         normalizePath(target, root),
		ranked:             ranked,
		result:             &Result{Target: target},
		contexts:           contexts,
		modeledFiles:       modeledFiles,
		storeWrites:        storeWriteStages(s.Stores),
		targetStores:       make(map[string]bool),
		directMap:          make(map[int]bool),
		downstreamMap:      make(map[int]bool),
		affectedCommandMap: make(map[string]bool),
	}
}

func phaseContexts(s *spec.Spec, root string) ([]phaseCtx, map[string]bool) {
	contexts := make([]phaseCtx, 0, len(s.Phases))
	modeled := make(map[string]bool)
	appendPhases := func(phases []spec.Phase, command string) {
		for index, phase := range phases {
			files := expandPhaseFiles(phase.Files, root)
			for _, file := range files {
				modeled[filepath.Clean(file)] = true
			}
			contexts = append(contexts, phaseCtx{phase, command, index + 1, extractAllStageLabels(phase.Stages), files})
		}
	}
	appendPhases(s.Phases, "")
	for _, command := range s.Commands {
		appendPhases(command.Phases, command.Name)
	}
	return contexts, modeled
}

func storeWriteStages(stores []spec.Store) map[string][]string {
	writes := make(map[string][]string)
	for _, store := range stores {
		for _, writer := range store.Writers {
			if !isReadAccess(writer.Access) {
				writes[store.Name] = append(writes[store.Name], writer.Stage)
			}
		}
	}
	return writes
}

func (a *analysisState) resolveTargets() {
	a.symbolFiles = goSymbolFiles(a.root, a.target, a.ranked)
	a.symbolCommands, a.commandScopedSymbol = scan.CommandsReachingFunction(a.root, a.target)
	imported := goImportedSymbolCommands(a.root, a.target, a.symbolFiles)
	a.preserveOwnerPhase = len(imported) > 0
	a.symbolCommands = dedupeStrings(append(a.symbolCommands, imported...))
	a.commandScopedSymbol = a.commandScopedSymbol || a.preserveOwnerPhase
	for _, file := range a.symbolFiles {
		a.result.Matches = append(a.result.Matches, Match{Type: TargetTypeSymbol, Name: a.target, Detail: fmt.Sprintf("in %s", file)})
	}
	for _, store := range a.spec.Stores {
		a.resolveStoreTarget(store)
	}
}

func (a *analysisState) resolveStoreTarget(store spec.Store) {
	if len(a.symbolFiles) == 0 && storeNameMatches(store.Name, a.target) {
		a.targetStores[store.Name] = true
	}
	for _, writer := range store.Writers {
		if !slicesContainsFold(writer.SourceSymbols, a.target) {
			continue
		}
		firstMatch := !a.targetStores[store.Name]
		a.targetStores[store.Name] = true
		if firstMatch {
			a.result.Matches = append(a.result.Matches, Match{Type: TargetTypeStore, Name: store.Name, Detail: fmt.Sprintf("path provenance in %s:%d", writer.SourceFile, writer.SourceLine)})
		}
	}
}

func (a *analysisState) findDirectPhases() {
	for index, phase := range a.contexts {
		fileMatch := a.matchesPhaseFile(phase)
		stageMatch := a.matchesPhaseStage(phase)
		if fileMatch || stageMatch {
			a.directMap[index] = true
		}
	}
}

func (a *analysisState) matchesPhaseFile(phase phaseCtx) bool {
	for _, file := range phase.allFiles {
		if fileMatches(file, a.normTarget, a.target) {
			a.result.Matches = append(a.result.Matches, Match{Type: TargetTypeFile, Name: file, Detail: fmt.Sprintf("matched phase %q", phase.phase.Name)})
			return true
		}
		if a.matchesSymbolFile(file) {
			return true
		}
	}
	return false
}

func (a *analysisState) matchesSymbolFile(file string) bool {
	if a.commandScopedSymbol && !a.preserveOwnerPhase {
		return false
	}
	for _, symbolFile := range a.symbolFiles {
		if fileMatches(file, symbolFile, symbolFile) {
			return true
		}
	}
	return false
}

func (a *analysisState) matchesPhaseStage(phase phaseCtx) bool {
	if len(a.symbolFiles) != 0 || len(a.targetStores) != 0 {
		return false
	}
	for _, stage := range phase.stages {
		if strings.EqualFold(stage, a.target) {
			a.result.Matches = append(a.result.Matches, Match{Type: TargetTypeStage, Name: stage, Detail: fmt.Sprintf("in phase %q", phase.phase.Name)})
			return true
		}
	}
	if strings.EqualFold(phase.phase.Name, a.target) {
		a.result.Matches = append(a.result.Matches, Match{Type: TargetTypeStage, Name: phase.phase.Name, Detail: "direct phase name match"})
		return true
	}
	return false
}

func (a *analysisState) findCommandScopedPhases() {
	if !a.commandScopedSymbol {
		return
	}
	for _, command := range a.symbolCommands {
		if index := a.commandPhase(command); index >= 0 {
			a.directMap[index] = true
		}
	}
}

func (a *analysisState) commandPhase(command string) int {
	selected := -1
	for index, phase := range a.contexts {
		if phase.cmdName != command {
			continue
		}
		if selected < 0 || slicesContainsFold(phase.stages, a.target) {
			selected = index
		}
		if slicesContainsFold(phase.stages, a.target) {
			break
		}
	}
	return selected
}

func (a *analysisState) findStoreWriterPhases() {
	for _, store := range a.spec.Stores {
		if !a.targetStores[store.Name] {
			continue
		}
		a.addStoreMatch(store)
		for index, phase := range a.contexts {
			if storeWritesPhase(store, a.storeWrites[store.Name], phase) {
				a.directMap[index] = true
			}
		}
	}
}

func (a *analysisState) addStoreMatch(store spec.Store) {
	if len(a.symbolFiles) == 0 && storeNameMatches(store.Name, a.target) {
		a.result.Matches = append(a.result.Matches, Match{Type: TargetTypeStore, Name: store.Name, Detail: "state store match"})
	}
}

func storeWritesPhase(store spec.Store, writerStages []string, phase phaseCtx) bool {
	for _, writer := range store.Writers {
		if !isReadAccess(writer.Access) && writer.SourceFile != "" && phaseHasFile(phase.allFiles, writer.SourceFile) {
			return true
		}
	}
	for _, stage := range writerStages {
		if len(phase.stages) == 0 && stageNamesEqual(phase.phase.Name, stage) {
			return true
		}
		if slicesContainsExact(phase.stages, stage) {
			return true
		}
	}
	return false
}

func slicesContainsExact(values []string, target string) bool {
	for _, value := range values {
		if stageNamesEqual(value, target) {
			return true
		}
	}
	return false
}

func (a *analysisState) addUnmodeledMatches() {
	for _, file := range rankedFileMatches(a.root, a.normTarget, a.target, a.ranked, a.modeledFiles) {
		a.result.Matches = append(a.result.Matches, Match{Type: TargetTypeFile, Name: file, Detail: "present in codebase but not modeled by a phase"})
	}
	a.result.Matches = dedupeMatches(a.result.Matches)
}
