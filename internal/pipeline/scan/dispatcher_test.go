package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/stretchr/testify/require"

	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// makeRankedFile is a test helper that builds a RankedFile with the given
// relative path, import path, and import list.
func makeRankedFile(relPath, importPath string, imports []string) symbols.RankedFile {
	return symbols.RankedFile{
		FileSymbols: &symbols.FileSymbols{
			Path:       "/root/" + relPath,
			Language:   literalGo,
			ImportPath: importPath,
			Imports:    imports,
		},
	}
}

func TestBuildImportGraph(t *testing.T) {
	t.Parallel()

	ranked := []symbols.RankedFile{
		makeRankedFile("cmd/app/main.go", "example.com/app/cmd/app", []string{
			"example.com/app/internal/commands",
		}),
		makeRankedFile("internal/commands/root.go", "example.com/app/internal/commands", []string{
			"example.com/app/internal/serve",
			"example.com/app/internal/scan",
		}),
		makeRankedFile("internal/serve/server.go", "example.com/app/internal/serve", nil),
		makeRankedFile("internal/scan/scan.go", "example.com/app/internal/scan", nil),
	}

	root := "/root"
	graph := buildImportGraph(ranked, root)

	// cmd/app/main.go should import internal/commands/root.go
	require.Contains(t, graph["cmd/app/main.go"], "internal/commands/root.go")

	// internal/commands/root.go should import serve and scan
	require.Contains(t, graph["internal/commands/root.go"], "internal/serve/server.go")
	require.Contains(t, graph["internal/commands/root.go"], "internal/scan/scan.go")

	// leaves have no outgoing edges
	require.Empty(t, graph["internal/serve/server.go"])
	require.Empty(t, graph["internal/scan/scan.go"])
}

func TestTransitiveImportClosure(t *testing.T) {
	t.Parallel()

	// Graph: foo → shared; bar → shared; shared → leaf
	graph := importGraphT{
		"internal/commands/foo.go": {"internal/spec/load.go"},
		"internal/commands/bar.go": {"internal/spec/load.go"},
		"internal/spec/load.go":    {"internal/spec/spec.go"},
		"internal/spec/spec.go":    {},
	}

	fooClosure := transitiveImportClosure("internal/commands/foo.go", graph)
	require.True(t, fooClosure["internal/commands/foo.go"])
	require.True(t, fooClosure["internal/spec/load.go"])
	require.True(t, fooClosure["internal/spec/spec.go"])

	barClosure := transitiveImportClosure("internal/commands/bar.go", graph)
	require.True(t, barClosure["internal/commands/bar.go"])
	require.True(t, barClosure["internal/spec/load.go"])
	require.True(t, barClosure["internal/spec/spec.go"])
}

func TestSharedInfrastructurePromotion(t *testing.T) {
	t.Parallel()

	// 3 commands; "internal/spec/spec.go" is reachable from all 3 (>3/2=1)
	// "internal/serve/server.go" is reachable from 1 (not majority).
	commandClosure := map[string]map[string]bool{
		"init": {
			"internal/commands/init.go": true,
			"internal/spec/spec.go":     true,
		},
		"render": {
			"internal/commands/render.go": true,
			"internal/spec/spec.go":       true,
			"internal/serve/server.go":    true,
		},
		"scan": {
			"internal/commands/scan.go": true,
			"internal/spec/spec.go":     true,
		},
	}

	threshold := len(commandClosure) / 2 // = 1

	fileCount := make(map[string]int)
	for _, closure := range commandClosure {
		for f := range closure {
			fileCount[f]++
		}
	}
	sharedFiles := make(map[string]bool)
	for f, count := range fileCount {
		if count > threshold {
			sharedFiles[f] = true
		}
	}

	// spec.go is shared (count=3 > threshold=1)
	require.True(t, sharedFiles["internal/spec/spec.go"], "spec.go should be shared (majority)")

	// server.go is NOT shared (count=1 == threshold=1, not >)
	require.False(t, sharedFiles["internal/serve/server.go"], "server.go should not be shared (not majority)")
}

func TestBuildDispatcherSpecRepresentsSharedModelFiles(t *testing.T) {
	t.Parallel()

	root := "/root"
	ranked := []symbols.RankedFile{
		makeFile(root, "cmd/app/main.go", 0,
			sym("main", "function", false),
		),
		makeFile(root, "internal/commands/root.go", 3,
			sym("NewRoot", "function", true),
		),
		makeFile(root, "internal/commands/init.go", 1,
			sym("newInitCmd", "function", false),
		),
		makeFile(root, "internal/commands/render.go", 1,
			sym("newRenderCmd", "function", false),
		),
		makeFile(root, "internal/commands/validate.go", 1,
			sym("newValidateCmd", "function", false),
		),
		makeFile(root, "internal/spec/load.go", 3,
			sym("Load", "function", true),
		),
		makeFile(root, "internal/spec/spec.go", 3,
			sym("applyDefaults", "function", false),
		),
		makeFile(root, "internal/spec/validate.go", 3,
			sym("Validate", "function", true),
		),
		makeFile(root, "internal/spec/paths.go", 3,
			sym("DataDir", "function", true),
		),
		makeFile(root, "internal/serve/server.go", 1,
			sym("Run", "function", true),
		),
	}
	info := dispatchInfo{
		commandFiles: map[string]string{
			"init":       "internal/commands/init.go",
			"render":     "internal/commands/render.go",
			baseValidate: "internal/commands/validate.go",
		},
		commandClosure: map[string]map[string]bool{
			"init": {
				"internal/commands/init.go": true,
				"internal/spec/load.go":     true,
				"internal/spec/spec.go":     true,
				"internal/spec/validate.go": true,
				"internal/spec/paths.go":    true,
			},
			"render": {
				"internal/commands/render.go": true,
				"internal/spec/load.go":       true,
				"internal/spec/spec.go":       true,
				"internal/spec/validate.go":   true,
				"internal/spec/paths.go":      true,
				"internal/serve/server.go":    true,
			},
			baseValidate: {
				"internal/commands/validate.go": true,
				"internal/spec/load.go":         true,
				"internal/spec/spec.go":         true,
				"internal/spec/validate.go":     true,
				"internal/spec/paths.go":        true,
			},
		},
		sharedFiles: map[string]bool{
			"internal/spec/load.go":     true,
			"internal/spec/spec.go":     true,
			"internal/spec/validate.go": true,
			"internal/spec/paths.go":    true,
		},
	}

	got, err := buildDispatcherSpec(root, "App", ranked, Config{MaxPhases: 9, MaxStages: 7}, info)
	require.NoError(t, err)

	phaseByName := make(map[string]spec.Phase)
	for _, phase := range got.Phases {
		phaseByName[phase.Name] = phase
	}
	require.Contains(t, phaseByName, "Spec")
	require.Contains(t, phaseByName["Spec"].Files, "internal/spec/load.go")
	require.Contains(t, phaseByName["Spec"].Files, "internal/spec/spec.go")
	require.Contains(t, phaseByName, "Validate")
	require.Contains(t, phaseByName["Validate"].Files, "internal/spec/validate.go")

	require.NotNil(t, got.Coverage)
	require.Equal(t, 10, got.Coverage.Total)
	require.Equal(t, 9, got.Coverage.Represented)
	require.NotContains(t, got.Coverage.Missing, "internal/spec/load.go")
	require.NotContains(t, got.Coverage.Missing, "internal/spec/spec.go")
	require.NotContains(t, got.Coverage.Missing, "internal/spec/validate.go")
	require.Contains(t, got.Coverage.Missing, "internal/spec/paths.go")
}

func TestSortedCommandNames_Deterministic(t *testing.T) {
	t.Parallel()

	commandFiles := map[string]string{
		"serve":      "internal/commands/serve.go",
		"init":       "internal/commands/init.go",
		"render":     "internal/commands/render.go",
		baseValidate: "internal/commands/validate.go",
	}

	names := sortedCommandNames(commandFiles)
	require.Equal(t, []string{"init", "render", "serve", baseValidate}, names)
}

func TestReadModulePath_Missing(t *testing.T) {
	t.Parallel()
	// Non-existent directory — should return false, no panic.
	_, ok := readModulePath(t.TempDir() + "/nonexistent")
	require.False(t, ok)
}

func TestNewCmdPattern_MatchesParameterisedConstructors(t *testing.T) {
	t.Parallel()

	// Both zero-arg and parameterised constructors must match — render/serve use open *bool.
	cases := []struct {
		line  string
		match bool
	}{
		{"func newRenderCmd(open *bool) *cobra.Command {", true},
		{"func newServeCmd(open *bool) *cobra.Command {", true},
		{"func newScanCmd() *cobra.Command {", true},
		{"func newInitCmd() *cobra.Command {", true},
		{"func NewIngestCmd(container func() *app.Container) *cobra.Command {", true},
		{"func NewEmbedCmd(container func() *app.Container) *cobra.Command {", true},
		{"func NewRunCmd(container func() *app.Container) *cobra.Command {", true},
		// root.go is excluded at the file-iteration level, not the regex level.
		{"func newRootCmd() *cobra.Command {", true},
		{"func NewRootCmd() *cobra.Command {", true},
		// Irrelevant lines must not match.
		{"func newRenderHelper(s string) error {", false},
		{"// newFakeCmd disabled", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.match, newCmdPattern.MatchString(tc.line), "line: %q", tc.line)
	}
}

func TestFindStdlibFlagCommandFiles(t *testing.T) {
	root := writeStdlibDispatcherFixture(t)

	commandFiles, localFiles := findStdlibFlagCommandFiles(root)
	require.Equal(t, map[string]string{
		"alpha": "main.go",
		"beta":  "main.go",
		"gamma": "main.go",
	}, commandFiles)
	require.Equal(t, map[string]bool{
		"main.go":         true,
		"alpha.go":        true,
		"alpha_detail.go": true,
	}, localFiles["alpha"])
	require.Equal(t, map[string]bool{
		"main.go":    true,
		"beta.go":    true,
		"beta_io.go": true,
	}, localFiles["beta"])
	require.NotContains(t, commandFiles, "legacy", "canonicalization aliases are not command lanes")
	require.NotContains(t, localFiles["alpha"], "unrelated.go", "function-valued parameters must not resolve to package functions")
	commands, found := CommandsReachingFunction(root, "alphaDetail")
	require.True(t, found)
	require.Equal(t, []string{"alpha"}, commands)
	_, found = CommandsReachingFunction(root, "unrelatedDetail")
	require.False(t, found)
	_, found = CommandsReachingFunction(root, "Name")
	require.False(t, found, "unresolved call names are not local functions")
}

func TestScanStdlibFlagCommandLanesIncludeSamePackageHandlers(t *testing.T) {
	root := writeStdlibDispatcherFixture(t)
	initScanFixtureGit(t, root)

	got, err := Scan(context.Background(), root, Config{})
	require.NoError(t, err)
	require.Len(t, got.Commands, 3)

	commands := make(map[string]spec.Command, len(got.Commands))
	for _, command := range got.Commands {
		commands[command.Name] = command
	}
	alphaFiles := commandPhaseFiles(commands["alpha"])
	betaFiles := commandPhaseFiles(commands["beta"])
	require.Equal(t, "Alpha", commands["alpha"].Phases[0].Name)
	require.Equal(t, "Beta", commands["beta"].Phases[0].Name)
	require.Contains(t, alphaFiles, "alpha.go")
	require.Contains(t, alphaFiles, "alpha_detail.go")
	require.NotContains(t, alphaFiles, "beta.go")
	require.NotContains(t, alphaFiles, "main.go", "the shared dispatcher belongs in the prelude")
	require.Contains(t, betaFiles, "beta.go")
	require.Contains(t, betaFiles, "beta_io.go")
	require.NotContains(t, betaFiles, "alpha.go")
	require.NotContains(t, betaFiles, "main.go", "the shared dispatcher belongs in the prelude")
}

func writeStdlibDispatcherFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeScanFixture(t, root, "go.mod", "module example.com/stdlib-dispatcher\n\ngo 1.26\n")
	writeScanFixture(t, root, "main.go", `package main

import "flag"

const (
	commandAlpha = "alpha"
	commandBeta = "beta"
	commandGamma = "gamma"
	commandLegacy = "legacy"
)

func main() {
	command := flag.CommandLine.Name()
	if command == "" {
		usageOnly()
		return
	}
	switch command {
	case commandAlpha:
		runAlpha()
	case commandBeta:
		runBeta()
	case commandGamma:
		runGamma()
	}
	f := flags{command: command}
	routeShared(&f)
}

type flags struct { command string }

func routeShared(f *flags) {
	switch f.command {
	case commandAlpha:
		alphaDetail()
	case commandBeta:
		writeBeta()
	}
}

func canonicalCommand(command string) string {
	if command == commandLegacy {
		return commandGamma
	}
	return command
}

func usageOnly() { unrelatedDetail() }
`)
	writeScanFixture(t, root, "alpha.go", `package main

func runAlpha() { runCallback(func() {}) }

func runCallback(run func()) {
	run()
	alphaDetail()
}
`)
	writeScanFixture(t, root, "alpha_detail.go", `package main

func alphaDetail() {}
`)
	writeScanFixture(t, root, "beta.go", `package main

func runBeta() { writeBeta() }
`)
	writeScanFixture(t, root, "beta_io.go", `package main

func writeBeta() {}
`)
	writeScanFixture(t, root, "gamma.go", `package main

func runGamma() {}
`)
	writeScanFixture(t, root, "unrelated.go", `package main

func run() { unrelatedDetail() }

func unrelatedDetail() {}
`)
	// A nested package-main helper must not be treated as application dispatch.
	writeScanFixture(t, root, filepath.Join("tools", "release", "main.go"), `package main

const (
	commandBuild = "build"
	commandPublish = "publish"
	commandVerify = "verify"
)
`)
	return root
}

func commandPhaseFiles(command spec.Command) map[string]bool {
	files := make(map[string]bool)
	for _, phase := range command.Phases {
		for _, file := range phase.Files {
			files[file] = true
		}
	}
	return files
}
