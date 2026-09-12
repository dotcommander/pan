package spec

// Coverage captures file representation metrics for a scanned spec.
// Derived data — not user-authored. Existing specs without this block round-trip cleanly.
type Coverage struct {
	Represented int      `yaml:"represented"`
	Total       int      `yaml:"total"`
	Missing     []string `yaml:"missing,omitempty"`
}

// IsLow reports whether coverage is below 90%. Returns false when Total is zero.
func (c *Coverage) IsLow() bool {
	return c != nil && c.Total > 0 && c.Represented*10 < c.Total*9
}

// Command groups the phases for a single dispatcher subcommand.
// Each command gets its own labeled lane in the rendered output.
type Command struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description,omitempty"`
	Phases      []Phase `yaml:"phases"`
}

// PhaseScope defines the execution scope of a phase in relation to a loop.
type PhaseScope string

const (
	// PhaseScopeUnknown represents an unclassified or unassigned phase scope.
	PhaseScopeUnknown PhaseScope = ""
	// PhaseScopeOnceBefore represents a phase executing once prior to loop entry.
	PhaseScopeOnceBefore PhaseScope = "once-before"
	// PhaseScopePerIteration represents a phase executing repeatedly within the loop body.
	PhaseScopePerIteration PhaseScope = "per-iteration"
	// PhaseScopeOnceAfter represents a phase executing once following loop exit.
	PhaseScopeOnceAfter PhaseScope = "once-after"
)

// Loop defines an iteration construct wrapping a subset of phases in the spec.
type Loop struct {
	Max            string   `yaml:"max,omitempty"`
	ExitConditions []string `yaml:"exit_conditions,omitempty"`
	PrePhases      []string `yaml:"pre_phases,omitempty"`
	BodyPhases     []string `yaml:"body_phases,omitempty"`
	PostPhases     []string `yaml:"post_phases,omitempty"`
}

// Spec is the top-level Pan document.
type Spec struct {
	Title      string    `yaml:"title"`
	Breadcrumb string    `yaml:"breadcrumb,omitempty"`
	Root       string    `yaml:"root,omitempty"`
	Loop       *Loop     `yaml:"loop,omitempty"`
	Phases     []Phase   `yaml:"phases,omitempty"`
	Commands   []Command `yaml:"commands,omitempty"`
	Stores     []Store   `yaml:"stores"`
	Coverage   *Coverage `yaml:"coverage,omitempty"`
}

// HasLoop reports whether the spec defines a non-empty loop construct.
func (s *Spec) HasLoop() bool {
	return s != nil && s.Loop != nil && len(s.Loop.BodyPhases) > 0
}

// LoopPhaseScope returns the PhaseScope of a phase by name according to s.Loop,
// or PhaseScopeUnknown if no loop is defined or the phase is unassigned.
func (s *Spec) LoopPhaseScope(phaseName string) PhaseScope {
	if s == nil || s.Loop == nil {
		return PhaseScopeUnknown
	}
	for _, name := range s.Loop.BodyPhases {
		if name == phaseName {
			return PhaseScopePerIteration
		}
	}
	for _, name := range s.Loop.PrePhases {
		if name == phaseName {
			return PhaseScopeOnceBefore
		}
	}
	for _, name := range s.Loop.PostPhases {
		if name == phaseName {
			return PhaseScopeOnceAfter
		}
	}
	return PhaseScopeUnknown
}

// PrePhases returns the subset of top-level phases matching s.Loop.PrePhases,
// or nil if no loop is defined.
func (s *Spec) PrePhases() []Phase {
	if !s.HasLoop() {
		return nil
	}
	nameSet := make(map[string]bool, len(s.Loop.PrePhases))
	for _, name := range s.Loop.PrePhases {
		nameSet[name] = true
	}
	var out []Phase
	for _, p := range s.Phases {
		if nameSet[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// BodyPhases returns the subset of top-level phases matching s.Loop.BodyPhases,
// or s.Phases if no loop is defined.
func (s *Spec) BodyPhases() []Phase {
	if !s.HasLoop() {
		return s.Phases
	}
	nameSet := make(map[string]bool, len(s.Loop.BodyPhases))
	for _, name := range s.Loop.BodyPhases {
		nameSet[name] = true
	}
	var out []Phase
	for _, p := range s.Phases {
		if nameSet[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// PostPhases returns the subset of top-level phases matching s.Loop.PostPhases,
// or nil if no loop is defined.
func (s *Spec) PostPhases() []Phase {
	if !s.HasLoop() {
		return nil
	}
	nameSet := make(map[string]bool, len(s.Loop.PostPhases))
	for _, name := range s.Loop.PostPhases {
		nameSet[name] = true
	}
	var out []Phase
	for _, p := range s.Phases {
		if nameSet[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// PhaseOrdinal returns the 1-based index of the named phase in s.Phases,
// or 0 if not found.
func (s *Spec) PhaseOrdinal(phaseName string) int {
	if s == nil {
		return 0
	}
	for i, p := range s.Phases {
		if p.Name == phaseName {
			return i + 1
		}
	}
	return 0
}

// AllPhases returns the prelude phases followed by every command's phases,
// flattened in declaration order. Single source of truth for "iterate every
// phase in this spec" — callers that previously did manual append loops
// should call this instead.
func (s *Spec) AllPhases() []Phase {
	out := make([]Phase, 0, len(s.Phases)+len(s.Commands))
	out = append(out, s.Phases...)
	for _, cmd := range s.Commands {
		out = append(out, cmd.Phases...)
	}
	return out
}

// PhaseKind is a closed-set enum of phase kinds. Zero value ("") means
// "unknown / not yet classified" and YAML-round-trips as omitempty.
type PhaseKind string

const (
	// PhaseKindUnknown means the phase kind is unset or unclassified.
	PhaseKindUnknown PhaseKind = ""
	// PhaseKindBoot is the process-startup phase kind.
	PhaseKindBoot PhaseKind = "Boot"
	// PhaseKindRoute is the request-routing phase kind.
	PhaseKindRoute PhaseKind = "Route"
	// PhaseKindParse is the input-parsing phase kind.
	PhaseKindParse PhaseKind = "Parse"
	// PhaseKindCheck is the validation-check phase kind.
	PhaseKindCheck PhaseKind = "Check"
	// PhaseKindRender is the output-rendering phase kind.
	PhaseKindRender PhaseKind = "Render"
	// PhaseKindServe is the long-running-serving phase kind.
	PhaseKindServe PhaseKind = "Serve"
	// PhaseKindWatch is the change-watching phase kind.
	PhaseKindWatch PhaseKind = "Watch"
	// PhaseKindEmit is the output-emitting phase kind.
	PhaseKindEmit PhaseKind = "Emit"
	// PhaseKindAnalyze is the analysis phase kind.
	PhaseKindAnalyze PhaseKind = "Analyze"
)

// Phase is one band in the pipeline.
type Phase struct {
	Name           string     `yaml:"name"`
	Kind           PhaseKind  `yaml:"kind,omitempty"`
	Scope          PhaseScope `yaml:"scope,omitempty"`
	Description    string     `yaml:"description,omitempty"`
	Stages         []Stage    `yaml:"stages"`
	Sources        []string   `yaml:"sources,omitempty"`
	Files          []string   `yaml:"files,omitempty"`
	TruncatedCount int        `yaml:"truncated_count,omitempty"`
}

// Stage is a tagged union: exactly one of Chip, Fork, Fanout, External is non-nil.
// UnmarshalYAML dispatches based on presence of "fork:" / "fanout:" / "external:" keys.
type Stage struct {
	Chip     *Chip
	Fork     *Fork
	Fanout   *Fanout
	External *External
}

// Stage shape discriminants shared by the YAML tagged-union encoding and
// Stage.Kind. Unexported because they are an internal encoding detail.
const (
	stageKindFork     = "fork"
	stageKindFanout   = "fanout"
	stageKindExternal = "external"
	stageKindChip     = "chip"
)

// Kind returns the discriminant string for this stage.
func (s Stage) Kind() string {
	switch {
	case s.Fork != nil:
		return stageKindFork
	case s.Fanout != nil:
		return stageKindFanout
	case s.External != nil:
		return stageKindExternal
	default:
		return stageKindChip
	}
}

// IsChip reports whether this stage is a plain chip.
func (s Stage) IsChip() bool { return s.Chip != nil }

// IsFork reports whether this stage is a fork.
func (s Stage) IsFork() bool { return s.Fork != nil }

// IsFanout reports whether this stage is a fanout.
func (s Stage) IsFanout() bool { return s.Fanout != nil }

// IsExternal reports whether this stage is an external (black-box) node.
func (s Stage) IsExternal() bool { return s.External != nil }

// ChipStyle is a closed-set enum of chip visual styles. Zero value ("")
// means "plain stage" (default rendering); YAML-round-trips as omitempty.
type ChipStyle string

const (
	// ChipStyleDefault renders a plain stage chip.
	ChipStyleDefault ChipStyle = ""
	// ChipStyleStage renders an interior processing-stage chip.
	ChipStyleStage ChipStyle = "stage"
	// ChipStyleIO renders an input/output boundary chip.
	ChipStyleIO ChipStyle = "io"
	// ChipStyleGate renders a decision-gate chip.
	ChipStyleGate ChipStyle = "gate"
	// ChipStyleBoot renders a process-boot chip.
	ChipStyleBoot ChipStyle = "boot"
)

// Chip is a single pipeline chip (plain stage, io, gate, or boot).
type Chip struct {
	Label      string    `yaml:"label"`
	Subtitle   string    `yaml:"subtitle,omitempty"`
	Style      ChipStyle `yaml:"style,omitempty"`
	Wide       bool      `yaml:"wide,omitempty"`
	SourceFile string    `yaml:"source_file,omitempty"`
	SourceLine int       `yaml:"source_line,omitempty"`
}

// Fork is a gate chip with 2+ conditional branches rendered vertically to its right.
type Fork struct {
	Gate     string   `yaml:"gate"`
	Branches []Branch `yaml:"branches"`
}

// Branch is one arm of a Fork.
type Branch struct {
	Condition  string    `yaml:"condition"`
	Label      string    `yaml:"label"`
	Subtitle   string    `yaml:"subtitle,omitempty"`
	Style      ChipStyle `yaml:"style,omitempty"`
	Wide       bool      `yaml:"wide,omitempty"`
	SourceFile string    `yaml:"source_file,omitempty"`
	SourceLine int       `yaml:"source_line,omitempty"`
}

// Fanout is a gate chip followed by a grid of N dispatch targets.
type Fanout struct {
	Gate    string   `yaml:"gate"`
	Targets []Target `yaml:"targets"`
}

// Target is one cell in a Fanout grid.
type Target struct {
	Flag     string `yaml:"flag"`
	Label    string `yaml:"label"`
	Sublabel string `yaml:"sublabel,omitempty"`
}

// External is a black-box node: a non-Go component, subprocess, service, queue,
// Lambda, or shell script. Rendered with a visually distinct chip style so the
// reader can tell at a glance that this stage lives outside the program.
// Kind is free-form — typical values are "subprocess", "service", "queue",
// "lambda", "shell" — but callers may use any string. Not enum-enforced.
type External struct {
	Label      string `yaml:"label"`
	Kind       string `yaml:"kind,omitempty"`
	Note       string `yaml:"note,omitempty"`
	SourceFile string `yaml:"source_file,omitempty"`
	SourceLine int    `yaml:"source_line,omitempty"`
}

// Store is a persistent artifact written off the spine.
type Store struct {
	Name    string   `yaml:"name"`
	Writers []Writer `yaml:"writers"`
}

// Writer links a stage to a store with access semantics.
type Writer struct {
	Stage         string   `yaml:"stage"`
	Access        string   `yaml:"access"` // "r/w", "append", "" (default: "written by")
	Note          string   `yaml:"note,omitempty"`
	SourceFile    string   `yaml:"source_file,omitempty"`
	SourceLine    int      `yaml:"source_line,omitempty"`
	SourceSymbols []string `yaml:"source_symbols,omitempty"`
}
