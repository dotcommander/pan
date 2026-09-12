package spec

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// preludeKinds reports whether a phase kind is allowed in a prelude
// (Spec.Phases when Commands are also present). Shared parser/checker
// infrastructure can live there when scan emits a mixed dispatcher spec;
// command-specific emit phases still belong inside a Command. The switch
// stays exhaustive over the PhaseKind enum.
func preludeKinds(kind PhaseKind) bool {
	switch kind {
	case PhaseKindUnknown, PhaseKindBoot, PhaseKindRoute, PhaseKindParse, PhaseKindCheck:
		return true
	case PhaseKindRender, PhaseKindServe, PhaseKindWatch, PhaseKindEmit, PhaseKindAnalyze:
		return false
	}
	return false
}

// Validate applies integrity checks per plan.md §validate using the spec's
// embedded root when present.
func Validate(s *Spec, specPath string) error {
	return ValidateWithRoot(s, specPath, "")
}

// ValidateWithRoot applies integrity checks per plan.md §validate.
// Returns a multi-error (errors.Join) if any check fails.
// Store-writer label mismatches and missing file refs are warnings only
// (logged), not errors — file refs are documentation hints, not invariants.
// File refs resolve against repoRootOverride, the spec's embedded root, or the
// nearest project root above the spec file, in that order.
func ValidateWithRoot(s *Spec, specPath, repoRootOverride string) error {
	logger := newValidationLogger()
	var errs []error
	errs = append(errs, validateSpecShape(s)...)
	errs = append(errs, validateCommands(s)...)
	errs = append(errs, validatePhaseList(s.Phases, "phases", logger)...)
	for _, cmd := range s.Commands {
		errs = append(errs, validatePhaseList(cmd.Phases, "command "+cmd.Name, logger)...)
	}
	errs = append(errs, validateLoop(s)...)
	projectRoot := ProjectRootForSpec(s, specPath, repoRootOverride)
	validateFileRefs(s, projectRoot, logger)
	validateStoreWriters(s, logger)
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// newValidationLogger returns the logger validation warnings go to. It
// mirrors slog's default text-on-stderr output without using the global
// default logger.
func newValidationLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// validateSpecShape enforces the top-level shape: a spec must have phases,
// commands, or both, and a mixed shape allows only prelude kinds in Phases.
func validateSpecShape(s *Spec) []error {
	hasPhases := len(s.Phases) > 0
	hasCommands := len(s.Commands) > 0
	if !hasPhases && !hasCommands {
		return []error{errors.New("spec must have phases or commands")}
	}
	if !hasPhases || !hasCommands {
		return nil
	}
	var errs []error
	for _, p := range s.Phases {
		if !preludeKinds(p.Kind) {
			errs = append(errs, fmt.Errorf("phase %q (kind=%q) cannot appear in prelude when commands are present; only Boot/Route/Parse/Check kinds allowed", p.Name, p.Kind))
		}
	}
	return errs
}

// validateCommands enforces command integrity: no empty commands and no
// duplicate names.
func validateCommands(s *Spec) []error {
	var errs []error
	cmdNames := map[string]int{}
	for _, cmd := range s.Commands {
		cmdNames[cmd.Name]++
		if len(cmd.Phases) == 0 {
			errs = append(errs, fmt.Errorf("command %q has no phases", cmd.Name))
		}
	}
	for name, n := range cmdNames {
		if n > 1 {
			errs = append(errs, fmt.Errorf("duplicate command name %q", name))
		}
	}
	return errs
}

// validatePhaseList applies the per-phase checks shared by the top-level
// prelude and every command's phase list.
func validatePhaseList(phases []Phase, context string, logger *slog.Logger) []error {
	var errs []error
	errs = append(errs, validatePhaseNames(phases, context)...)
	errs = append(errs, validateStageCardinality(phases, context)...)
	errs = append(errs, validateStageLabels(phases, context, logger)...)
	errs = append(errs, validatePhaseScopes(phases, context)...)
	return errs
}

// validatePhaseNames reports duplicate phase names within one phase list.
func validatePhaseNames(phases []Phase, context string) []error {
	seen := map[string]int{}
	for _, p := range phases {
		seen[p.Name]++
	}
	var errs []error
	for name, n := range seen {
		if n > 1 {
			errs = append(errs, fmt.Errorf("%s: phase name %q used %d times", context, name, n))
		}
	}
	return errs
}

// validateStageCardinality enforces the fork and fanout population minimums.
func validateStageCardinality(phases []Phase, context string) []error {
	var errs []error
	for _, p := range phases {
		for i, st := range p.Stages {
			if st.Fork != nil && len(st.Fork.Branches) < 2 {
				errs = append(errs, fmt.Errorf("%s: phase %q stage[%d]: fork has %d branches, need >= 2", context, p.Name, i, len(st.Fork.Branches)))
			}
			if st.Fanout != nil && len(st.Fanout.Targets) < 1 {
				errs = append(errs, fmt.Errorf("%s: phase %q stage[%d]: fanout has no targets", context, p.Name, i))
			}
		}
	}
	return errs
}

// validateStageLabels reports stages with empty labels. An external stage
// with an empty label is a warning, not an error — it still renders, just
// without a name.
func validateStageLabels(phases []Phase, context string, logger *slog.Logger) []error {
	var errs []error
	for _, p := range phases {
		for i, st := range p.Stages {
			switch {
			case st.Chip != nil && strings.TrimSpace(st.Chip.Label) == "":
				errs = append(errs, fmt.Errorf("%s: phase %q stage[%d]: chip has empty label", context, p.Name, i))
			case st.Fork != nil && strings.TrimSpace(st.Fork.Gate) == "":
				errs = append(errs, fmt.Errorf("%s: phase %q stage[%d]: fork has empty gate", context, p.Name, i))
			case st.Fanout != nil && strings.TrimSpace(st.Fanout.Gate) == "":
				errs = append(errs, fmt.Errorf("%s: phase %q stage[%d]: fanout has empty gate", context, p.Name, i))
			case st.External != nil && strings.TrimSpace(st.External.Label) == "":
				logger.Warn("external stage has empty label", "context", context, "phase", p.Name, "stage_index", i)
			}
		}
	}
	return errs
}

// validatePhaseScopes rejects phase scope values outside the closed set.
func validatePhaseScopes(phases []Phase, context string) []error {
	var errs []error
	for _, p := range phases {
		switch p.Scope {
		case PhaseScopeUnknown, PhaseScopeOnceBefore, PhaseScopePerIteration, PhaseScopeOnceAfter:
		default:
			errs = append(errs, fmt.Errorf("%s: phase %q has invalid scope %q (must be %q, %q, or %q)", context, p.Name, p.Scope, PhaseScopeOnceBefore, PhaseScopePerIteration, PhaseScopeOnceAfter))
		}
	}
	return errs
}

// validateFileRefs warns (never errors) when a phase file reference resolves
// to nothing on disk — refs are documentation hints, not invariants. Literal
// and glob refs both resolve against projectRoot.
func validateFileRefs(s *Spec, projectRoot string, logger *slog.Logger) {
	for _, p := range s.AllPhases() {
		for _, f := range p.Files {
			matches, isGlob := resolveFileRef(f, projectRoot)
			if isGlob {
				if len(matches) == 0 {
					logger.Warn("phase file glob matched no files",
						"phase", p.Name,
						"pattern", f,
					)
				}
				continue
			}
			// literal path — single entry in matches
			if _, err := os.Stat(matches[0]); err != nil {
				logger.Warn("phase file ref not found",
					"phase", p.Name,
					"file", f,
					"resolved", matches[0],
				)
			}
		}
	}
}

// validateStoreWriters warns when a store writer references a stage label
// that matches no phase or stage label — descriptive labels like
// "collect (HTTP)" don't literally match chip labels in cross-repo examples.
func validateStoreWriters(s *Spec, logger *slog.Logger) {
	labels := collectLabels(s)
	for _, store := range s.Stores {
		for _, w := range store.Writers {
			if w.Stage == "" {
				continue
			}
			if !labels[w.Stage] {
				logger.Warn("store writer does not match any stage label",
					"store", store.Name,
					"writer", w.Stage,
				)
			}
		}
	}
}

// ProjectRootForSpec returns the root used to resolve source file references.
// The CLI may pass repoRootOverride from --repo; scanned specs can carry Root
// when they are written outside the repo; older specs fall back to ancestor
// marker discovery from the spec file's directory.
func ProjectRootForSpec(s *Spec, specPath, repoRootOverride string) string {
	root := strings.TrimSpace(repoRootOverride)
	if root == "" && s != nil {
		root = strings.TrimSpace(s.Root)
	}
	if root == "" {
		root = inferProjectRoot(specPath)
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}

// inferProjectRoot finds the root for a spec with no explicit root: the
// nearest project root at or above the spec file, falling back to the
// working directory when it is itself a project repository.
func inferProjectRoot(specPath string) string {
	specDir := filepath.Dir(specPath)
	candidate := FindProjectRoot(specDir)
	if candidate == specDir && hasProjectMarker(".") {
		return cwdRoot()
	}
	return candidate
}

// cwdRoot returns the absolute working directory, or "." when it cannot be
// resolved.
func cwdRoot() string {
	if abs, err := filepath.Abs("."); err == nil {
		return abs
	}
	return "."
}

// projectRootMarkers returns the marker files that identify a project root.
func projectRootMarkers() []string {
	return []string{"go.mod", ".git", "package.json", "Cargo.toml"}
}

// hasProjectMarker reports whether dir contains any project-root marker.
func hasProjectMarker(dir string) bool {
	for _, marker := range projectRootMarkers() {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// FindProjectRoot walks up from start (at most 8 levels) looking for a marker
// file that identifies the project root: go.mod, .git, package.json, or Cargo.toml.
// Returns the first ancestor directory containing any marker; falls back to
// start if no marker is found within 8 levels.
func FindProjectRoot(start string) string {
	dir := start
	for i := 0; i < 8; i++ {
		if hasProjectMarker(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return start
}

func collectLabels(s *Spec) map[string]bool {
	m := map[string]bool{}
	for _, p := range s.AllPhases() {
		m[p.Name] = true
		for _, st := range p.Stages {
			switch {
			case st.Chip != nil:
				m[st.Chip.Label] = true
			case st.Fork != nil:
				m[st.Fork.Gate] = true
				for _, b := range st.Fork.Branches {
					m[b.Label] = true
				}
			case st.Fanout != nil:
				m[st.Fanout.Gate] = true
				for _, t := range st.Fanout.Targets {
					m[t.Label] = true
				}
			case st.External != nil:
				m[st.External.Label] = true
			}
		}
	}
	return m
}

func validateLoop(s *Spec) []error {
	if s.Loop == nil {
		return nil
	}
	phaseNames := make(map[string]int, len(s.Phases))
	for idx, p := range s.Phases {
		phaseNames[p.Name] = idx
	}

	var errs []error
	errs = append(errs, validateLoopMembership(s, phaseNames)...)
	errs = append(errs, validateLoopOrdering(s, phaseNames)...)
	errs = append(errs, validateLoopScopeConsistency(s)...)
	return errs
}

func validateLoopMembership(s *Spec, phaseNames map[string]int) []error {
	var errs []error
	if len(s.Loop.BodyPhases) == 0 {
		errs = append(errs, errors.New("loop must define at least one body phase in body_phases"))
	}

	seenInLoop := make(map[string]string)
	checkGroup := func(groupName string, names []string) {
		for _, name := range names {
			if _, ok := phaseNames[name]; !ok {
				errs = append(errs, fmt.Errorf("loop %s references unknown phase %q", groupName, name))
			}
			if existingGroup, duplicate := seenInLoop[name]; duplicate {
				errs = append(errs, fmt.Errorf("loop phase %q cannot appear in both %s and %s", name, existingGroup, groupName))
			} else {
				seenInLoop[name] = groupName
			}
		}
	}

	checkGroup("pre_phases", s.Loop.PrePhases)
	checkGroup("body_phases", s.Loop.BodyPhases)
	checkGroup("post_phases", s.Loop.PostPhases)

	// Coverage: every top-level phase must be accounted for in pre, body, or post.
	for _, p := range s.Phases {
		if _, ok := seenInLoop[p.Name]; !ok {
			errs = append(errs, fmt.Errorf("loop must cover all phases: phase %q is not in pre_phases, body_phases, or post_phases", p.Name))
		}
	}
	return errs
}

type loopPhaseBounds struct {
	lastPre   int
	firstBody int
	lastBody  int
	firstPost int
}

func calculateLoopBounds(s *Spec, phaseNames map[string]int) loopPhaseBounds {
	b := loopPhaseBounds{
		lastPre:   -1,
		firstBody: len(s.Phases) + 1,
		lastBody:  -1,
		firstPost: len(s.Phases) + 1,
	}
	for _, name := range s.Loop.PrePhases {
		if idx, ok := phaseNames[name]; ok && idx > b.lastPre {
			b.lastPre = idx
		}
	}
	for _, name := range s.Loop.BodyPhases {
		if idx, ok := phaseNames[name]; ok {
			if idx < b.firstBody {
				b.firstBody = idx
			}
			if idx > b.lastBody {
				b.lastBody = idx
			}
		}
	}
	for _, name := range s.Loop.PostPhases {
		if idx, ok := phaseNames[name]; ok && idx < b.firstPost {
			b.firstPost = idx
		}
	}
	return b
}

func validateLoopOrdering(s *Spec, phaseNames map[string]int) []error {
	var errs []error
	b := calculateLoopBounds(s, phaseNames)

	if b.lastPre >= 0 && b.firstBody <= len(s.Phases) && b.lastPre >= b.firstBody {
		errs = append(errs, errors.New("loop pre_phases must appear before body_phases in phases list"))
	}
	if b.lastBody >= 0 && b.firstPost <= len(s.Phases) && b.lastBody >= b.firstPost {
		errs = append(errs, errors.New("loop body_phases must appear before post_phases in phases list"))
	}
	if len(s.Loop.BodyPhases) > 0 && b.lastBody >= b.firstBody {
		if (b.lastBody - b.firstBody + 1) != len(s.Loop.BodyPhases) {
			errs = append(errs, errors.New("loop body_phases must be a contiguous block of phases"))
		}
	}
	return errs
}

func validateLoopScopeConsistency(s *Spec) []error {
	var errs []error
	for _, p := range s.Phases {
		if p.Scope == "" {
			continue
		}
		expectedScope := s.LoopPhaseScope(p.Name)
		if expectedScope != PhaseScopeUnknown && p.Scope != expectedScope {
			errs = append(errs, fmt.Errorf("phase %q has scope %q but loop assigns it %q", p.Name, p.Scope, expectedScope))
		}
	}
	return errs
}
