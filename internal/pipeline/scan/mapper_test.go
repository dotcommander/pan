package scan

import (
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeFile builds a synthetic RankedFile for use in tests.
func makeFile(root, rel string, importedBy int, syms ...symbols.Symbol) symbols.RankedFile {
	return symbols.RankedFile{
		FileSymbols: &symbols.FileSymbols{
			Path:     filepath.Join(root, filepath.FromSlash(rel)),
			Language: literalGo,
			Symbols:  syms,
		},
		ImportedBy: importedBy,
	}
}

// sym builds a Symbol for tests.
func sym(name, kind string, exported bool) symbols.Symbol {
	return symbols.Symbol{Name: name, Kind: kind, Exported: exported}
}

func TestGroupPhases_BasicLayout(t *testing.T) {
	t.Parallel()
	root := "/proj"
	ranked := []symbols.RankedFile{
		makeFile(root, "cmd/repoflow/main.go", 0,
			sym("main", "function", false),
		),
		makeFile(root, "internal/serve/server.go", 2,
			sym("Run", "function", true),
			sym("Config", "struct", true),
			sym("registerWatches", "function", false),
		),
		makeFile(root, "internal/render/render.go", 3,
			sym("Render", "function", true),
			sym("ToBytes", "function", true),
		),
	}

	phases := groupPhases(ranked, root, Config{MaxPhases: 7, MaxStages: 7})

	require.Len(t, phases, 3)

	// Boot phase from cmd/repoflow/ — kind overrides pkg name to "Boot"
	assert.Equal(t, "Boot", phases[0].Name)
	assert.Equal(t, spec.PhaseKindBoot, phases[0].Kind)
	assert.Contains(t, phases[0].Files[0], "cmd/repoflow/main.go")

	// internal/ phases appear after cmd/
	names := make([]string, len(phases))
	for i, p := range phases {
		names[i] = p.Name
	}
	assert.Contains(t, names, "Serve")
	// internal/render is its own phase with kind Render (not folded into Spec)
	assert.Contains(t, names, "Render")
	assert.NotContains(t, names, "Spec", "internal/spec not in this test fixture — no spec files")
}

func TestGroupPhases_KindInference(t *testing.T) {
	t.Parallel()
	root := "/proj"
	ranked := []symbols.RankedFile{
		makeFile(root, "internal/render/render.go", 1,
			sym("Render", "function", true),
		),
		makeFile(root, "internal/validate/validate.go", 1,
			sym("Check", "function", true),
		),
		makeFile(root, "internal/spec/spec.go", 1,
			sym("Load", "function", true),
		),
	}

	phases := groupPhases(ranked, root, Config{MaxPhases: 7, MaxStages: 7})

	kindFor := func(name string) spec.PhaseKind {
		for _, p := range phases {
			if p.Name == name {
				return p.Kind
			}
		}
		return ""
	}

	// internal/render is its own distinct phase with kind Render,
	// separate from internal/spec which remains kind Parse.
	assert.Equal(t, spec.PhaseKindRender, kindFor("Render"), "internal/render must be a standalone Render phase")
	assert.Equal(t, spec.PhaseKindCheck, kindFor("Validate"))
	assert.Equal(t, spec.PhaseKindParse, kindFor("Spec"))
}

func TestExtractStages_StyleInference(t *testing.T) {
	t.Parallel()
	root := "/proj"
	files := []symbols.RankedFile{
		makeFile(root, "internal/serve/server.go", 2,
			sym("NewServer", "function", true),
			sym("HandleRequest", "function", true),
			sym(baseValidate, "function", false), // unexported — excluded
			sym("return value", "function", true),
			sym("float64", "function", true),
			sym("Process", "function", true),
		),
	}

	stages, _ := extractStages(files, 10, "")

	require.GreaterOrEqual(t, len(stages), 3)

	// NewServer must be first with style "boot"
	require.NotNil(t, stages[0].Chip)
	assert.Equal(t, "NewServer", stages[0].Chip.Label)
	assert.Equal(t, spec.ChipStyleBoot, stages[0].Chip.Style)

	// HandleRequest has style "io"
	labels := make([]string, len(stages))
	styles := make(map[string]spec.ChipStyle)
	for i, s := range stages {
		if s.Chip != nil {
			labels[i] = s.Chip.Label
			styles[s.Chip.Label] = s.Chip.Style
		}
	}
	assert.Equal(t, spec.ChipStyleIO, styles["HandleRequest"])
	assert.Equal(t, spec.ChipStyleDefault, styles["Process"]) // plain chip

	// validate must not appear (unexported)
	assert.NotContains(t, labels, baseValidate)
	assert.NotContains(t, labels, "return value")
	assert.NotContains(t, labels, "float64")
}

func TestGroupPhases_MaxPhasesCap(t *testing.T) {
	t.Parallel()
	root := "/proj"

	// Build 6 distinct internal packages, each with one file and one symbol.
	var ranked []symbols.RankedFile
	pkgs := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	for _, pkg := range pkgs {
		ranked = append(ranked, makeFile(root,
			"internal/"+pkg+"/"+pkg+".go", 0,
			sym("Do", "function", true),
		))
	}

	phases := groupPhases(ranked, root, Config{MaxPhases: 4, MaxStages: 7})

	assert.LessOrEqual(t, len(phases), 4, "should merge down to maxPhases")
}

func TestExtractStages_Truncation(t *testing.T) {
	t.Parallel()
	root := "/proj"

	// 5 exported functions but maxStages=3 → 3 stages returned, truncated=2.
	// "+N more" must never appear as a chip — count is returned separately.
	files := []symbols.RankedFile{
		makeFile(root, "internal/x/x.go", 0,
			sym("A", "function", true),
			sym("B", "function", true),
			sym("C", "function", true),
			sym("D", "function", true),
			sym("E", "function", true),
		),
	}

	stages, truncated := extractStages(files, 3, "")

	require.Len(t, stages, 3)
	assert.Equal(t, 2, truncated)
	for _, s := range stages {
		require.NotNil(t, s.Chip)
		assert.NotContains(t, s.Chip.Label, "more", "truncation must not appear as a chip")
	}
}

func TestGroupKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rel  string
		want string
	}{
		{"main.go", ""},
		{"cmd/repoflow/main.go", "cmd/repoflow"},
		{"internal/serve/server.go", "internal/serve"},
		{"pkg/util/util.go", "pkg/util"},
		{"internal/serve/deep/extra.go", "internal/serve"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, groupKey(tc.rel), "groupKey(%q)", tc.rel)
	}
}

// TestGroupPhases_SpecPackageSplit verifies that internal/spec/ is split into
// a Load phase (load.go, spec.go) and a Validate phase (validate.go), and that
// paths.go is suppressed entirely.
func TestGroupPhases_SpecPackageSplit(t *testing.T) {
	t.Parallel()
	root := "/proj"

	ranked := []symbols.RankedFile{
		makeFile(root, "internal/spec/load.go", 3,
			sym("Load", "function", true),
			sym("UnmarshalYAML", "method", true),
		),
		makeFile(root, "internal/spec/spec.go", 3,
			sym("applyDefaults", "function", false),
		),
		makeFile(root, "internal/spec/validate.go", 2,
			sym("Validate", "function", true),
			sym("FindProjectRoot", "function", true),
		),
		makeFile(root, "internal/spec/paths.go", 1,
			sym("UserDir", "function", true),
			sym("DataDir", "function", true),
			sym("ScanOutputPath", "function", true),
		),
	}

	phases := groupPhases(ranked, root, Config{MaxPhases: 7, MaxStages: 7})

	// Find phases by kind/name.
	phaseNames := make([]string, len(phases))
	for i, p := range phases {
		phaseNames[i] = p.Name
	}

	// Must have a Validate phase.
	require.Contains(t, phaseNames, "Validate", "expected a Validate phase")

	// Validate phase must contain validate.go.
	var validatePhase spec.Phase
	for _, p := range phases {
		if p.Name == "Validate" {
			validatePhase = p
			break
		}
	}
	assert.Contains(t, validatePhase.Files, "internal/spec/validate.go")

	// paths.go must not appear in any phase's files.
	for _, p := range phases {
		assert.NotContains(t, p.Files, "internal/spec/paths.go",
			"paths.go should be suppressed from phase %q", p.Name)
	}

	// paths.go symbols must not appear as stages in any phase.
	for _, p := range phases {
		for _, s := range p.Stages {
			if s.Chip != nil {
				label := s.Chip.Label
				assert.NotEqual(t, "UserDir", label, "UserDir should be suppressed")
				assert.NotEqual(t, "DataDir", label, "DataDir should be suppressed")
				assert.NotEqual(t, "ScanOutputPath", label, "ScanOutputPath should be suppressed")
			}
		}
	}
}

// TestExtractStages_SkipsUtilityMethods verifies that accessor/getter methods
// (As*, Single, String, etc.) are excluded from pipeline stages, while plain
// exported functions pass through.
func TestExtractStages_SkipsUtilityMethods(t *testing.T) {
	t.Parallel()
	root := "/proj"

	// sym with param/result counts for zero-arg/single-result filter.
	symPR := func(name, kind string, exported bool, params, results int) symbols.Symbol {
		return symbols.Symbol{
			Name:        name,
			Kind:        kind,
			Exported:    exported,
			ParamCount:  params,
			ResultCount: results,
		}
	}

	files := []symbols.RankedFile{
		makeFile(root, "internal/spec/spec.go", 3,
			sym("Load", "function", true),            // exported function — keep
			symPR("Single", "method", true, 0, 1),    // zero-arg, 1 result method — skip
			symPR("AsSummary", "method", true, 0, 1), // As* method — skip
			sym("String", "method", true),            // String method — skip
		),
	}

	stages, _ := extractStages(files, 10, "Parse")

	labels := make([]string, 0, len(stages))
	for _, s := range stages {
		if s.Chip != nil {
			labels = append(labels, s.Chip.Label)
		}
	}

	assert.Contains(t, labels, "Load", "Load function should be included")
	assert.NotContains(t, labels, "Single", "Single method should be suppressed")
	assert.NotContains(t, labels, "AsSummary", "AsSummary method should be suppressed")
	assert.NotContains(t, labels, "String", "String method should be suppressed")
}

// TestExtractStages_UtilitySymbolDenyList verifies that symbols on the
// utilitySymbolNames deny-list are suppressed even when their file is not on
// the utilityFileStems list (e.g. FindProjectRoot declared in validate.go).
func TestExtractStages_UtilitySymbolDenyList(t *testing.T) {
	t.Parallel()
	root := "/proj"

	// validate.go is NOT a utility file by stem, but FindProjectRoot is a
	// utility symbol by name and must be suppressed.
	files := []symbols.RankedFile{
		makeFile(root, "internal/spec/validate.go", 2,
			sym("Validate", "function", true),
			sym("FindProjectRoot", "function", true),
			sym("ScanOutputPath", "function", true),
			sym("Resolve", "function", true),
			sym("DataDir", "function", true),
			sym("UserDir", "function", true),
			sym("ProjectName", "function", true),
			sym("AsSummary", "method", true),
			sym("Single", "method", true),
		),
	}

	stages, _ := extractStages(files, 20, "Check")

	labels := make([]string, 0, len(stages))
	for _, s := range stages {
		if s.Chip != nil {
			labels = append(labels, s.Chip.Label)
		}
	}

	// Validate should survive.
	assert.Contains(t, labels, "Validate", "Validate should be kept")

	// All deny-list symbols must be absent.
	for _, name := range []string{
		"FindProjectRoot", "ScanOutputPath", "Resolve",
		"DataDir", "UserDir", "ProjectName", "AsSummary", "Single",
	} {
		assert.NotContains(t, labels, name, "%s should be suppressed by deny-list", name)
	}
}

// TestIsUtilityFile verifies the deny-list matches expected stems.
func TestIsUtilityFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{"/proj/internal/spec/paths.go", true},
		{"/proj/internal/spec/dirs.go", true},
		{"/proj/internal/util/util.go", true},
		{"/proj/internal/common/helpers.go", true},
		{"/proj/internal/spec/load.go", false},
		{"/proj/internal/spec/validate.go", false},
		{"/proj/internal/spec/spec.go", false},
		{"/proj/internal/serve/server.go", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isUtilityFile(tc.path), "isUtilityFile(%q)", tc.path)
	}
}

// TestIsBlockedStdlibCall is a regression guard: every entry in
// blockedStdlibCalls must be recognised, and no false-positive for
// first-class project symbols must be introduced.
func TestIsBlockedStdlibCall(t *testing.T) {
	t.Parallel()

	blocked := []struct{ pkg, name string }{
		{"http", "NotFound"},
		{"log", "Warn"}, {"log", "Warnf"}, {"log", "Error"}, {"log", "Errorf"},
		{"log", "Fatal"}, {"log", "Fatalf"}, {"log", "Print"}, {"log", "Printf"}, {"log", "Println"},
		{"fmt", "Print"}, {"fmt", "Printf"}, {"fmt", "Println"},
		{"fmt", "Fprint"}, {"fmt", "Fprintf"}, {"fmt", "Fprintln"},
		{"fmt", "Sprint"}, {"fmt", "Sprintf"}, {"fmt", "Errorf"},
		{"errors", "New"}, {"errors", "Is"}, {"errors", "As"},
		{"errors", "Unwrap"}, {"errors", "Join"},
		{"slog", "Info"}, {"slog", "Warn"}, {"slog", "Error"}, {"slog", "Debug"},
	}
	for _, tc := range blocked {
		assert.True(t, isBlockedStdlibCall(tc.pkg, tc.name),
			"isBlockedStdlibCall(%q, %q) should be true", tc.pkg, tc.name)
	}

	allowed := []struct{ pkg, name string }{
		{"", "Println"},       // empty pkg — not a qualified call
		{"myapp", "NotFound"}, // project-local symbol with same name as stdlib
		{"log", ""},           // empty name — no-op
		{"http", "Handle"},    // not in the block list
	}
	for _, tc := range allowed {
		assert.False(t, isBlockedStdlibCall(tc.pkg, tc.name),
			"isBlockedStdlibCall(%q, %q) should be false", tc.pkg, tc.name)
	}
}

// TestStdlibNoiseFilter_ForkBranches verifies that switch branches whose
// first statement calls a blocked stdlib function (http.NotFound, log.Warn,
// fmt.Println, errors.New) do not surface as chip labels in fork branches.
func TestStdlibNoiseFilter_ForkBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	// A dispatcher whose switch arms call stdlib noise functions.
	rel := writeFile(t, dir, "handler.go", `package handler

func Dispatch(kind string) {
	switch kind {
	case "a":
		http.NotFound(nil, nil)
	case "b":
		log.Warn("something")
	case "c":
		fmt.Println("y")
	case "d":
		errors.New("z")
	case "e":
		doRealWork()
	}
}

func doRealWork() {}
`)

	phases := phase1([]string{rel}, "Dispatch")
	out := detectForks(dir, phases)

	require.Len(t, out, 1)
	// If the fork survived (enough non-noise branches), verify no blocked labels.
	for _, s := range out[0].Stages {
		if s.Fork == nil {
			continue
		}
		for _, b := range s.Fork.Branches {
			assert.NotEqual(t, "NotFound", b.Label, "http.NotFound must not appear as a branch label")
			assert.NotEqual(t, "Warn", b.Label, "log.Warn must not appear as a branch label")
			assert.NotEqual(t, "Println", b.Label, "fmt.Println must not appear as a branch label")
			assert.NotEqual(t, "New", b.Label, "errors.New must not appear as a branch label")
		}
	}
}

// TestGroupSortOrder verifies that well-known pipeline packages receive
// distinct priorities so that mergePhases cannot collapse them.
// internal/render now has priority 9 (same as internal/spec), keeping Parse
// and Render phases adjacent and protected from aggressive merging.
// The ordering is: 9 < 10 < 11.
func TestGroupSortOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key  string
		want int
	}{
		{"", 40},
		{"cmd/repoflow", 0},
		{"internal/commands", 5},
		{"internal/spec", 9},
		{"internal/render", 9}, // distinct phase, same priority tier as internal/spec
		{"internal/serve", 10},
		{"internal/scan", 10},
		{"internal/spec/validate", 11},
		{"pkg/util", 20},
		{"unknown/thing", 30},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, groupSortOrder(tc.key), "groupSortOrder(%q)", tc.key)
	}
}
