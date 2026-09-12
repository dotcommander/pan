package impact

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleSpec() *spec.Spec {
	return &spec.Spec{
		Title: "TestApp",
		Phases: []spec.Phase{
			{
				Name:  "Ingest",
				Kind:  spec.PhaseKindBoot,
				Files: []string{"cmd/app/main.go", "internal/config/config.go"},
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "loadConfig"}},
					{Chip: &spec.Chip{Label: "parseFlags"}},
				},
			},
			{
				Name:  "Process",
				Kind:  spec.PhaseKindParse,
				Files: []string{"internal/process/parser.go"},
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "parseItems"}},
				},
			},
		},
		Commands: []spec.Command{
			{
				Name: "export",
				Phases: []spec.Phase{
					{
						Name:  "Transform",
						Files: []string{"internal/export/transform.go"},
						Stages: []spec.Stage{
							{Chip: &spec.Chip{Label: "transformData"}},
						},
					},
					{
						Name:  "Emit",
						Files: []string{"internal/export/writer.go"},
						Stages: []spec.Stage{
							{Chip: &spec.Chip{Label: "writeOutput"}},
						},
					},
				},
			},
			{
				Name: "sync",
				Phases: []spec.Phase{
					{
						Name:  "Network",
						Files: []string{"internal/sync/client.go"},
						Stages: []spec.Stage{
							{Chip: &spec.Chip{Label: "syncCloud"}},
						},
					},
				},
			},
		},
		Stores: []spec.Store{
			{
				Name: "cache.db",
				Writers: []spec.Writer{
					{Stage: "parseItems", Access: "write"},
					{Stage: "transformData", Access: "read"},
				},
			},
		},
	}
}

func TestImpactAnalyze_DirectFileMatchInPrelude(t *testing.T) {
	s := sampleSpec()
	res, err := Analyze(context.Background(), s, "/root", "internal/config/config.go", nil)
	require.NoError(t, err)

	assert.Equal(t, "internal/config/config.go", res.Target)
	assert.NotEmpty(t, res.Matches)
	assert.Len(t, res.DirectPhases, 1)
	assert.Equal(t, "Ingest", res.DirectPhases[0].Name)

	// Since Ingest is prelude, all subsequent prelude and command phases are downstream
	downstreamNames := make([]string, 0, len(res.DownstreamPhases))
	for _, p := range res.DownstreamPhases {
		downstreamNames = append(downstreamNames, p.Name)
	}
	assert.Contains(t, downstreamNames, "Process")
	assert.Contains(t, downstreamNames, "Transform")
	assert.Contains(t, downstreamNames, "Emit")
	assert.Contains(t, downstreamNames, "Network")

	assert.Equal(t, "critical", res.Severity)
}

func TestImpactAnalyze_CommandLaneIsolation(t *testing.T) {
	s := sampleSpec()
	res, err := Analyze(context.Background(), s, "/root", "internal/export/transform.go", nil)
	require.NoError(t, err)

	require.Len(t, res.DirectPhases, 1)
	assert.Equal(t, "Transform", res.DirectPhases[0].Name)
	assert.Equal(t, "export", res.DirectPhases[0].Command)

	// Downstream in export command should only be Emit
	require.Len(t, res.DownstreamPhases, 1)
	assert.Equal(t, "Emit", res.DownstreamPhases[0].Name)
	assert.Equal(t, "export", res.DownstreamPhases[0].Command)

	assert.Equal(t, []string{"export"}, res.AffectedCommands)
	assert.Equal(t, "moderate", res.Severity)
}

func TestImpactAnalyze_StageMatchAndStoreImpact(t *testing.T) {
	s := sampleSpec()
	res, err := Analyze(context.Background(), s, "/root", "parseItems", nil)
	require.NoError(t, err)

	require.Len(t, res.DirectPhases, 1)
	assert.Equal(t, "Process", res.DirectPhases[0].Name)

	// parseItems writes cache.db, which is read by transformData in Transform
	assert.Contains(t, res.AffectedStores, "cache.db")

	downstreamNames := make([]string, 0, len(res.DownstreamPhases))
	for _, p := range res.DownstreamPhases {
		downstreamNames = append(downstreamNames, p.Name)
	}
	assert.Contains(t, downstreamNames, "Transform")
}

func TestImpactAnalyze_StoreTarget(t *testing.T) {
	s := sampleSpec()
	res, err := Analyze(context.Background(), s, "/root", "cache.db", nil)
	require.NoError(t, err)

	// parseItems writes cache.db -> direct phase Process
	require.Len(t, res.DirectPhases, 1)
	assert.Equal(t, "Process", res.DirectPhases[0].Name)

	// transformData reads cache.db -> downstream phase Transform
	downstreamNames := make([]string, 0, len(res.DownstreamPhases))
	for _, p := range res.DownstreamPhases {
		downstreamNames = append(downstreamNames, p.Name)
	}
	assert.Contains(t, downstreamNames, "Transform")
	assert.Contains(t, res.AffectedStores, "cache.db")
}

func TestImpactAnalyze_UnmodeledFileIsUnknown(t *testing.T) {
	s := sampleSpec()
	ranked := []symbols.RankedFile{{FileSymbols: &symbols.FileSymbols{Path: "internal/shared/flags.go", Language: "go"}}}
	res, err := Analyze(context.Background(), s, "/root", "internal/shared/flags.go", ranked)
	require.NoError(t, err)
	assert.Equal(t, "unknown", res.Severity)
	assert.NotEmpty(t, res.Matches)
	jsonData, err := res.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), `"affected_stores": null`)
	assert.Contains(t, string(jsonData), `"affected_commands": null`)
}

func TestImpactAnalyze_UnexportedGoSymbol(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n\nfunc resolveProvider() {}\n"), 0o600))
	s := &spec.Spec{Phases: []spec.Phase{{Name: "Run", Files: []string{"main.go"}}}}
	ranked := []symbols.RankedFile{{FileSymbols: &symbols.FileSymbols{Path: "main.go", Language: "go"}}}
	res, err := Analyze(context.Background(), s, root, "resolveProvider", ranked)
	require.NoError(t, err)
	assert.Equal(t, "isolated", res.Severity)
	require.Len(t, res.DirectPhases, 1)
	assert.Equal(t, TargetTypeSymbol, res.Matches[0].Type)
}

func TestImpactAnalyze_SymbolTakesPrecedenceOverSameNamedPhase(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "provider.go"), []byte("package main\n\ntype Provider interface{}\n"), 0o600))
	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "Provider", Files: []string{"other.go"}},
			{Name: "Runtime", Files: []string{"provider.go"}},
		},
	}
	ranked := []symbols.RankedFile{{FileSymbols: &symbols.FileSymbols{Path: "provider.go", Language: "go"}}}

	res, err := Analyze(context.Background(), s, root, "Provider", ranked)
	require.NoError(t, err)
	require.Len(t, res.DirectPhases, 1)
	assert.Equal(t, "Runtime", res.DirectPhases[0].Name)
	require.Len(t, res.Matches, 1)
	assert.Equal(t, TargetTypeSymbol, res.Matches[0].Type)
}

func TestImpactAnalyze_SymbolDoesNotInferCallersFromPhaseOrder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "provider.go"), []byte("package main\n\nfunc resolveProvider() {}\n"), 0o600))
	s := &spec.Spec{Phases: []spec.Phase{
		{Name: "Provider", Files: []string{"provider.go"}},
		{Name: "Output", Files: []string{"output.go"}},
	}}
	ranked := []symbols.RankedFile{{FileSymbols: &symbols.FileSymbols{Path: "provider.go", Language: "go"}}}

	res, err := Analyze(context.Background(), s, root, "resolveProvider", ranked)
	require.NoError(t, err)
	require.Len(t, res.DirectPhases, 1)
	assert.Empty(t, res.DownstreamPhases)
}

func TestImpactAnalyze_StoreWriterMayUsePhaseName(t *testing.T) {
	s := &spec.Spec{
		Phases: []spec.Phase{{Name: "Cache", Files: []string{"cache.go"}}},
		Stores: []spec.Store{{Name: "models-dev.json", Writers: []spec.Writer{{Stage: "Cache", Access: "w"}}}},
	}
	res, err := Analyze(context.Background(), s, "/root", "models-dev.json", nil)
	require.NoError(t, err)
	require.Len(t, res.DirectPhases, 1)
	assert.Equal(t, "Cache", res.DirectPhases[0].Name)
	assert.Equal(t, []string{"models-dev.json"}, res.AffectedStores)
}

func TestImpactAnalyze_StageNamesRequireExactMatch(t *testing.T) {
	s := sampleSpec()
	res, err := Analyze(context.Background(), s, "/root", "Config", nil)
	require.NoError(t, err)
	assert.Empty(t, res.DirectPhases)
	assert.Equal(t, "none", res.Severity)
}

func TestImpactAnalyze_StoreStageNamesAreCaseSensitive(t *testing.T) {
	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "Config", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "Load"}}}},
		},
		Commands: []spec.Command{{Name: "models", Phases: []spec.Phase{
			{Name: "Catalog", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "load"}}}},
		}}},
		Stores: []spec.Store{{Name: "catalog.json", Writers: []spec.Writer{{Stage: "load", Access: "r"}}}},
	}
	res, err := Analyze(context.Background(), s, "/root", "catalog.json", nil)
	require.NoError(t, err)
	require.Len(t, res.DownstreamPhases, 1)
	assert.Equal(t, "Catalog", res.DownstreamPhases[0].Name)
}

func TestImpactAnalyze_FieldSymbolConnectsResolvedStore(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "cache.go"), []byte("package main\n\ntype service struct { cachePath string }\n"), 0o600))
	s := &spec.Spec{
		Phases: []spec.Phase{{Name: "Cache", Files: []string{"cache.go"}}},
		Stores: []spec.Store{{Name: "~/.config/app/cache.json", Writers: []spec.Writer{{
			Stage:         "Cache",
			Access:        "w",
			SourceFile:    "cache.go",
			SourceLine:    3,
			SourceSymbols: []string{"cachePath"},
		}}}},
	}
	ranked := []symbols.RankedFile{{FileSymbols: &symbols.FileSymbols{Path: "cache.go", Language: "go"}}}

	res, err := Analyze(context.Background(), s, root, "cachePath", ranked)
	require.NoError(t, err)
	require.Contains(t, res.AffectedStores, "~/.config/app/cache.json")
	require.Len(t, res.DirectPhases, 1)
	require.Equal(t, "Cache", res.DirectPhases[0].Name)
	require.Len(t, res.Matches, 2)
}

func TestImpactAnalyze_ImportedSymbolConnectsCommandCallers(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "provider"), 0o755))
	mainSource := `package main

import (
	"flag"
	"example.com/app/internal/provider"
)

const (
	commandAlpha = "alpha"
	commandBeta = "beta"
	commandGamma = "gamma"
)

func main() {
	command := flag.CommandLine.Name()
	switch command {
	case commandAlpha:
		runAlpha()
	case commandBeta:
		runBeta()
	case commandGamma:
		runGamma()
	}
}

func runAlpha(value provider.Provider) {}
func runBeta() {}
func runGamma() {}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSource), 0o600))
	providerPath := filepath.Join(root, "internal", "provider", "provider.go")
	require.NoError(t, os.WriteFile(providerPath, []byte("package provider\n\ntype Provider interface{}\n"), 0o600))
	s := &spec.Spec{
		Phases: []spec.Phase{{Name: "Provider", Files: []string{"internal/provider/provider.go"}}},
		Commands: []spec.Command{
			{Name: "alpha", Phases: []spec.Phase{{Name: "Alpha"}}},
			{Name: "beta", Phases: []spec.Phase{{Name: "Beta"}}},
			{Name: "gamma", Phases: []spec.Phase{{Name: "Gamma"}}},
		},
	}
	ranked := []symbols.RankedFile{
		{FileSymbols: &symbols.FileSymbols{Path: "main.go", Language: "go"}},
		{FileSymbols: &symbols.FileSymbols{Path: "internal/provider/provider.go", Language: "go"}},
	}

	res, err := Analyze(context.Background(), s, root, "Provider", ranked)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha"}, res.AffectedCommands)
	directNames := make([]string, 0, len(res.DirectPhases))
	for _, phase := range res.DirectPhases {
		directNames = append(directNames, phase.Name)
	}
	require.ElementsMatch(t, []string{"Provider", "Alpha"}, directNames)
}

func TestImpactAnalyze_TextAndMarkdown(t *testing.T) {
	s := sampleSpec()
	res, err := Analyze(context.Background(), s, "/root", "internal/export/writer.go", nil)
	require.NoError(t, err)

	text := res.Text()
	assert.Contains(t, text, "IMPACT BLAST RADIUS: internal/export/writer.go")
	assert.Contains(t, text, "Emit")

	md := res.Markdown()
	assert.Contains(t, md, "# Impact Analysis: `internal/export/writer.go`")
	assert.Contains(t, md, "Emit")

	jsonData, err := res.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), `"target": "internal/export/writer.go"`)
}

func TestImpactAnalyze_LoopDownstream(t *testing.T) {
	s := &spec.Spec{
		Title: "LoopApp",
		Loop: &spec.Loop{
			Max:        "10",
			PrePhases:  []string{"Boot"},
			BodyPhases: []string{"Fetch", "Process"},
			PostPhases: []string{"Done"},
		},
		Phases: []spec.Phase{
			{Name: "Boot", Scope: spec.PhaseScopeOnceBefore, Files: []string{"boot.go"}},
			{Name: "Fetch", Scope: spec.PhaseScopePerIteration, Files: []string{"fetch.go"}},
			{Name: "Process", Scope: spec.PhaseScopePerIteration, Files: []string{"process.go"}},
			{Name: "Done", Scope: spec.PhaseScopeOnceAfter, Files: []string{"done.go"}},
		},
	}

	// Match "process.go" which is in "Process" (ordinal 3).
	// In a non-loop system, "Fetch" (ordinal 2) would NOT be downstream.
	// But in a loop body, "Process" re-enters "Fetch" on the next iteration!
	res, err := Analyze(context.Background(), s, "/root", "process.go", nil)
	require.NoError(t, err)
	require.Len(t, res.DirectPhases, 1)
	assert.Equal(t, "Process", res.DirectPhases[0].Name)

	downstreamNames := make([]string, 0, len(res.DownstreamPhases))
	for _, p := range res.DownstreamPhases {
		downstreamNames = append(downstreamNames, p.Name)
	}
	assert.Contains(t, downstreamNames, "Fetch", "Loop body phase Fetch should be downstream on next iteration")
	assert.Contains(t, downstreamNames, "Done", "Post phase Done should be downstream")
}
