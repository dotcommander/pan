package scan

import (
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// Three named phase-refinement chains encode the three current scan shapes
// (including the prelude/command subset differences, which are intentional and
// must not be silently normalized). Each step transforms the phase state; the
// runner is the single source of truth for ordering. The presets are local
// values inside their runners so no package-level mutable state exists.
//
// Execution is strictly sequential: every step depends on the previous phase
// slice. Parallel dispatch is not applicable and intentionally not provided.

// phaseState is the context threaded through a phase-refinement chain.
//   - payload: phases (the entity being refined), with ranked and cfg as inputs
//   - metadata: root
//
// stores is filled only by detectStores, which is invoked outside the chain
// presets because its dual (stores, phases) signature and its
// across-all-phases semantics in the dispatcher shape do not fit the
// single-agent transform contract.
type phaseState struct {
	root   string
	ranked []symbols.RankedFile
	cfg    Config
	phases []spec.Phase
}

// phaseStep is one transform over the phase payload.
type phaseStep func(phaseState) phaseState

// runChain applies a sequence of steps to the state in order and returns the
// resulting state. It is the phase-chain edge: ordering lives
// here, not at call sites.
func runChain(s phaseState, chain []phaseStep) phaseState {
	for _, step := range chain {
		s = step(s)
	}
	return s
}

func groupPhasesStep(s phaseState) phaseState {
	s.phases = groupPhases(s.ranked, s.root, s.cfg)
	return s
}

func splitLargePhasesStep(s phaseState) phaseState {
	s.phases = splitLargePhases(s.phases, s.ranked, s.root, s.cfg)
	return s
}

func fillEmptyPhasesStep(s phaseState) phaseState {
	s.phases = fillEmptyPhases(s.root, s.phases, s.cfg.MaxStages)
	return s
}

// mergePhasesStep preserves the shared "only when over cap" policy.
func mergePhasesStep(s phaseState) phaseState {
	if len(s.phases) > s.cfg.MaxPhases {
		s.phases = mergePhases(s.phases, s.cfg.MaxPhases)
	}
	return s
}

func deduplicatePhaseNamesStep(s phaseState) phaseState {
	s.phases = deduplicatePhaseNames(s.phases)
	return s
}

func orderByCallChainStep(s phaseState) phaseState {
	s.phases = orderByCallChain(s.root, s.phases)
	return s
}

func detectForksStep(s phaseState) phaseState {
	s.phases = detectForks(s.root, s.phases)
	return s
}

func detectFanoutsStep(s phaseState) phaseState {
	s.phases = detectFanouts(s.root, s.phases)
	return s
}

func detectEmitForkStep(s phaseState) phaseState {
	s.phases = detectEmitFork(s.root, s.phases)
	return s
}

func enrichSubtitlesStep(s phaseState) phaseState {
	s.phases = enrichSubtitles(s.root, s.phases)
	return s
}

func detectSourcesStep(s phaseState) phaseState {
	s.phases = detectSources(s.root, s.phases)
	return s
}

// The three presets below are the single source of truth for "what the phase
// chain is" in each shape. They encode current behavior exactly. The prelude
// and command presets are deliberately narrower than the full chain; that
// asymmetry is preserved here as explicit, reviewable data rather than
// implicit call order, and must not be "fixed" without an explicit
// behavior-change request.

// runFlatChain runs the flat-shape phase chain and returns the refined phases.
// detectStores and detectSources are intentionally NOT in the chain; the caller
// invokes them in that order after the chain (detectStores mutates phases, then
// detectSources adds provenance) to preserve the original flat-shape ordering.
func runFlatChain(root string, ranked []symbols.RankedFile, cfg Config) []spec.Phase {
	chain := []phaseStep{
		groupPhasesStep,
		splitLargePhasesStep,
		fillEmptyPhasesStep,
		mergePhasesStep,
		deduplicatePhaseNamesStep,
		orderByCallChainStep,
		detectForksStep,
		detectFanoutsStep,
		detectEmitForkStep,
		enrichSubtitlesStep,
	}
	s := runChain(phaseState{root: root, ranked: ranked, cfg: cfg, phases: nil}, chain)
	return s.phases
}

// runPreludeChain runs the dispatcher prelude chain. It intentionally omits
// split/fill/merge/fork/emit-fork/subtitle/order.
func runPreludeChain(root string, ranked []symbols.RankedFile, cfg Config) []spec.Phase {
	chain := []phaseStep{
		groupPhasesStep,
		deduplicatePhaseNamesStep,
		detectFanoutsStep,
		detectSourcesStep,
	}
	s := runChain(phaseState{root: root, ranked: ranked, cfg: cfg, phases: nil}, chain)
	return s.phases
}

// runCommandChain runs groupPhasesPerCommand's prefix followed by the command
// tail. It preserves the per-command MaxPhases cap applied by the caller. The
// tail intentionally omits detectEmitFork (see runFlatChain).
func runCommandChain(root string, ranked []symbols.RankedFile, cfg Config) []spec.Phase {
	prefix := []phaseStep{
		groupPhasesStep,
		splitLargePhasesStep,
		fillEmptyPhasesStep,
		mergePhasesStep,
		deduplicatePhaseNamesStep,
		orderByCallChainStep,
	}
	tail := []phaseStep{
		detectForksStep,
		detectFanoutsStep,
		enrichSubtitlesStep,
		detectSourcesStep,
	}
	s := phaseState{root: root, ranked: ranked, cfg: cfg, phases: nil}
	s = runChain(s, prefix)
	s = runChain(s, tail)
	return s.phases
}
