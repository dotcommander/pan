package scan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestMerge_PreservesHumanFields(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Title:      "My Hand-Authored Title",
		Breadcrumb: "My > Breadcrumb",
	}
	scanned := &spec.Spec{
		Title:      "Auto-Generated Title",
		Breadcrumb: "Auto > Breadcrumb",
	}

	result := Merge(existing, scanned)

	assert.Equal(t, "My Hand-Authored Title", result.Title)
	assert.Equal(t, "My > Breadcrumb", result.Breadcrumb)
}

func TestMerge_AddsNewPhase(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Title: "Test",
		Phases: []spec.Phase{
			{Name: "Alpha"},
		},
	}
	scanned := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "Alpha"},
			{Name: "Beta"},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Phases, 2)
	assert.Equal(t, "Alpha", result.Phases[0].Name)
	assert.Equal(t, "Beta", result.Phases[1].Name)
}

func TestMerge_AddsNewFiles(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "Alpha", Files: []string{"a.go", "b.go"}},
		},
	}
	scanned := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "Alpha", Files: []string{"a.go", "c.go"}},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Phases, 1)
	assert.Equal(t, []string{"a.go", "b.go", "c.go"}, result.Phases[0].Files)
}

func TestMerge_AddsNewStages(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Alpha",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "Existing Chip"}},
				},
			},
		},
	}
	scanned := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Alpha",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "Existing Chip"}},
					{Chip: &spec.Chip{Label: "New Chip"}},
				},
			},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Phases, 1)
	require.Len(t, result.Phases[0].Stages, 2)
	assert.Equal(t, "Existing Chip", result.Phases[0].Stages[0].Chip.Label)
	assert.Equal(t, "New Chip", result.Phases[0].Stages[1].Chip.Label)
}

func TestMerge_PreservesForkFanout(t *testing.T) {
	t.Parallel()

	fork := &spec.Fork{
		Gate: "Branch Gate",
		Branches: []spec.Branch{
			{Condition: "yes", Label: "Path A"},
			{Condition: "no", Label: "Path B"},
		},
	}
	fanout := &spec.Fanout{
		Gate:    "Dispatch Gate",
		Targets: []spec.Target{{Flag: "alpha", Label: "Worker A"}},
	}

	existing := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Alpha",
				Stages: []spec.Stage{
					{Fork: fork},
					{Fanout: fanout},
				},
			},
		},
	}
	scanned := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Alpha",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "Some New Chip"}},
				},
			},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Phases[0].Stages, 3)
	assert.NotNil(t, result.Phases[0].Stages[0].Fork)
	assert.Equal(t, "Branch Gate", result.Phases[0].Stages[0].Fork.Gate)
	assert.NotNil(t, result.Phases[0].Stages[1].Fanout)
	assert.Equal(t, "Dispatch Gate", result.Phases[0].Stages[1].Fanout.Gate)
	assert.Equal(t, "Some New Chip", result.Phases[0].Stages[2].Chip.Label)
}

func TestMerge_NoDuplicateStages(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Alpha",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "Shared Chip"}},
				},
			},
		},
	}
	scanned := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Alpha",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "Shared Chip"}},
				},
			},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Phases[0].Stages, 1)
	assert.Equal(t, "Shared Chip", result.Phases[0].Stages[0].Chip.Label)
}

func TestMerge_StoresPreserveNotes(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Stores: []spec.Store{
			{
				Name: "db",
				Writers: []spec.Writer{
					{Stage: "save", Access: "r/w", Note: "human note"},
				},
			},
		},
	}
	scanned := &spec.Spec{
		Stores: []spec.Store{
			{
				Name: "db",
				Writers: []spec.Writer{
					{Stage: "save", Access: "r/w", SourceFile: "store.go", SourceLine: 42, SourceSymbols: []string{"dbPath"}},
					{Stage: "load", Access: "r/w"},
				},
			},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Stores, 1)
	require.Len(t, result.Stores[0].Writers, 2)
	// Original writer preserves its note.
	assert.Equal(t, "human note", result.Stores[0].Writers[0].Note)
	assert.Equal(t, "store.go", result.Stores[0].Writers[0].SourceFile)
	assert.Equal(t, 42, result.Stores[0].Writers[0].SourceLine)
	assert.Equal(t, []string{"dbPath"}, result.Stores[0].Writers[0].SourceSymbols)
	// New writer from scan is added.
	assert.Equal(t, "load", result.Stores[0].Writers[1].Stage)
}

func TestMerge_NewStore(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Stores: []spec.Store{
			{Name: "cache"},
		},
	}
	scanned := &spec.Spec{
		Stores: []spec.Store{
			{Name: "cache"},
			{Name: "db"},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Stores, 2)
	assert.Equal(t, "cache", result.Stores[0].Name)
	assert.Equal(t, "db", result.Stores[1].Name)
}

func TestMerge_PreservesScannedCommandsCoverageAndFanoutTargets(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Title:      "Repoflow",
		Breadcrumb: "cmd/repoflow → Dispatch",
		Phases: []spec.Phase{{
			Name: "Dispatch",
			Stages: []spec.Stage{{Fanout: &spec.Fanout{
				Gate: gateSubcommand,
				Targets: []spec.Target{
					{Flag: "scan", Label: "newScanCmd"},
				},
			}}},
			Files: []string{"internal/commands/root.go"},
		}},
	}
	scanned := &spec.Spec{
		Phases: []spec.Phase{{
			Name: "Dispatch",
			Stages: []spec.Stage{{Fanout: &spec.Fanout{
				Gate: gateSubcommand,
				Targets: []spec.Target{
					{Flag: "scan", Label: "newScanCmd"},
					{Flag: "map", Label: "newMapCmd"},
				},
			}}},
			Files: []string{"internal/commands/root.go", "internal/commands/map.go"},
		}},
		Commands: []spec.Command{{
			Name: "map",
			Phases: []spec.Phase{{
				Name:  "Map",
				Files: []string{"internal/commands/map.go"},
			}},
		}},
		Coverage: &spec.Coverage{
			Represented: 31,
			Total:       35,
			Missing:     []string{"internal/spec/load.go"},
		},
	}

	result := Merge(existing, scanned)

	require.Len(t, result.Phases, 1)
	assert.Equal(t, []string{"internal/commands/root.go", "internal/commands/map.go"}, result.Phases[0].Files)
	require.Len(t, result.Phases[0].Stages, 1)
	require.NotNil(t, result.Phases[0].Stages[0].Fanout)
	assert.Equal(t, []spec.Target{
		{Flag: "scan", Label: "newScanCmd"},
		{Flag: "map", Label: "newMapCmd"},
	}, result.Phases[0].Stages[0].Fanout.Targets)
	require.Len(t, result.Commands, 1)
	assert.Equal(t, "map", result.Commands[0].Name)
	require.NotNil(t, result.Coverage)
	assert.Equal(t, 31, result.Coverage.Represented)
	assert.Equal(t, []string{"internal/spec/load.go"}, result.Coverage.Missing)
}

func TestMerge_PreservesExternalStage(t *testing.T) {
	t.Parallel()

	external := &spec.External{
		Label: "Python scorer",
		Kind:  "subprocess",
		Note:  "scoring.py",
	}
	existing := &spec.Spec{
		Phases: []spec.Phase{{
			Name: "Scoring",
			Stages: []spec.Stage{
				{Chip: &spec.Chip{Label: "prep"}},
				{External: external},
			},
		}},
	}
	// Scan never emits external stages — simulate a scan that found chips but
	// no knowledge of the external component.
	scanned := &spec.Spec{
		Phases: []spec.Phase{{
			Name: "Scoring",
			Stages: []spec.Stage{
				{Chip: &spec.Chip{Label: "prep"}},
				{Chip: &spec.Chip{Label: "postproc"}},
			},
		}},
	}
	result := Merge(existing, scanned)
	require.Len(t, result.Phases, 1)
	require.Len(t, result.Phases[0].Stages, 3)
	// External stage survives at its original index (index 1).
	require.NotNil(t, result.Phases[0].Stages[1].External)
	assert.Equal(t, "Python scorer", result.Phases[0].Stages[1].External.Label)
	assert.Equal(t, "subprocess", result.Phases[0].Stages[1].External.Kind)
	// New chip from scan is appended.
	require.NotNil(t, result.Phases[0].Stages[2].Chip)
	assert.Equal(t, "postproc", result.Phases[0].Stages[2].Chip.Label)
}

func TestFindMatchingPhase_ByName(t *testing.T) {
	t.Parallel()

	scanned := []spec.Phase{
		{Name: "Alpha"},
		{Name: "Beta"},
		{Name: "Gamma"},
	}
	ep := spec.Phase{Name: "Beta"}

	idx := findMatchingPhase(ep, scanned)
	assert.Equal(t, 1, idx)
}

func TestFindMatchingPhase_ByFileOverlap(t *testing.T) {
	t.Parallel()

	// ep has files a,b,c; scanned[1] has a,b,d — 2/3 overlap (>50%).
	ep := spec.Phase{
		Name:  "Old Name",
		Files: []string{"a.go", "b.go", "c.go"},
	}
	scanned := []spec.Phase{
		{Name: "Unrelated", Files: []string{"x.go", "y.go"}},
		{Name: "Renamed Phase", Files: []string{"a.go", "b.go", "d.go"}},
	}

	idx := findMatchingPhase(ep, scanned)
	assert.Equal(t, 1, idx)
}

func TestMerge_PreservesLoopAndScope(t *testing.T) {
	t.Parallel()

	existing := &spec.Spec{
		Title: "Loop Pipeline",
		Loop: &spec.Loop{
			Max:        "10",
			BodyPhases: []string{"Worker"},
		},
		Phases: []spec.Phase{
			{
				Name:  "Worker",
				Scope: spec.PhaseScopePerIteration,
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "work"}},
				},
			},
		},
	}
	scanned := &spec.Spec{
		Title: "Scanned Pipeline",
		Phases: []spec.Phase{
			{
				Name: "Worker",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "work"}},
					{Chip: &spec.Chip{Label: "extra"}},
				},
			},
		},
	}

	result := Merge(existing, scanned)
	require.NotNil(t, result.Loop)
	assert.Equal(t, "10", result.Loop.Max)
	require.Len(t, result.Phases, 1)
	assert.Equal(t, spec.PhaseScopePerIteration, result.Phases[0].Scope)
}
