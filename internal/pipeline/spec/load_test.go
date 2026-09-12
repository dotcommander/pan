package spec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "top level", yaml: "title: x\nphazes: []\n", want: "phazes"},
		{name: "phase", yaml: "title: x\nphases:\n  - name: p\n    stajes: []\n", want: "stajes"},
		{name: "stage discriminator", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - fok: {}\n", want: "fok"},
		{name: "chip", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - label: x\n        lable: y\n", want: "lable"},
		{name: "nested fork", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - fork:\n          gate: g\n          branchs: []\n", want: "branchs"},
		{name: "fork branch", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - fork:\n          gate: g\n          branches:\n            - condition: yes\n              label: x\n              lable: y\n", want: "lable"},
		{name: "fanout", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - fanout:\n          gate: g\n          targgets: []\n", want: "targgets"},
		{name: "fanout target", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - fanout:\n          gate: g\n          targets:\n            - flag: --x\n              label: x\n              lable: y\n", want: "lable"},
		{name: "external", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - external:\n          label: x\n          kynd: service\n", want: "kynd"},
		{name: "mixed stage shapes", yaml: "title: x\nphases:\n  - name: p\n    stages:\n      - fork:\n          gate: g\n          branches: []\n        label: x\n", want: "exactly one stage shape"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "unknown.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.yaml), 0o600))
			_, err := spec.Load(path)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestLoad_Minimal(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "minimal.yaml"))
	require.NoError(t, err)
	require.Equal(t, "Minimal", s.Title)
	require.Len(t, s.Phases, 1)
	require.Len(t, s.Phases[0].Stages, 2)
	require.NotNil(t, s.Phases[0].Stages[0].Chip)
	require.NotNil(t, s.Phases[0].Stages[1].Chip)
	require.Equal(t, "in", s.Phases[0].Stages[0].Chip.Label)
	require.Equal(t, "out", s.Phases[0].Stages[1].Chip.Label)
}

func TestStage_UnmarshalYAML_Chip(t *testing.T) {
	t.Parallel()
	raw := `label: x`
	var st spec.Stage
	err := yaml.Unmarshal([]byte(raw), &st)
	require.NoError(t, err)
	require.NotNil(t, st.Chip)
	require.Nil(t, st.Fork)
	require.Nil(t, st.Fanout)
	require.Equal(t, "x", st.Chip.Label)
}

func TestStage_UnmarshalYAML_Fork(t *testing.T) {
	t.Parallel()
	raw := `
fork:
  gate: "Is valid?"
  branches:
    - { condition: "yes", label: "proceed" }
    - { condition: "no", label: "reject" }
`
	var st spec.Stage
	err := yaml.Unmarshal([]byte(raw), &st)
	require.NoError(t, err)
	require.NotNil(t, st.Fork)
	require.Nil(t, st.Chip)
	require.Nil(t, st.Fanout)
	require.Equal(t, "Is valid?", st.Fork.Gate)
	require.Len(t, st.Fork.Branches, 2)
}

func TestStage_UnmarshalYAML_Fanout(t *testing.T) {
	t.Parallel()
	raw := `
fanout:
  gate: "Dispatch"
  targets:
    - { flag: "--a", label: "alpha" }
    - { flag: "--b", label: "beta" }
`
	var st spec.Stage
	err := yaml.Unmarshal([]byte(raw), &st)
	require.NoError(t, err)
	require.NotNil(t, st.Fanout)
	require.Nil(t, st.Chip)
	require.Nil(t, st.Fork)
	require.Equal(t, "Dispatch", st.Fanout.Gate)
	require.Len(t, st.Fanout.Targets, 2)
}

func TestStage_UnmarshalYAML_External(t *testing.T) {
	t.Parallel()
	raw := `
external:
  label: "Python scorer"
  kind: subprocess
  note: "runs scoring.py via exec.Command"
`
	var st spec.Stage
	err := yaml.Unmarshal([]byte(raw), &st)
	require.NoError(t, err)
	require.NotNil(t, st.External)
	require.Nil(t, st.Chip)
	require.Nil(t, st.Fork)
	require.Nil(t, st.Fanout)
	require.Equal(t, "Python scorer", st.External.Label)
	require.Equal(t, "subprocess", st.External.Kind)
	require.Equal(t, "runs scoring.py via exec.Command", st.External.Note)
}

func TestStage_UnmarshalYAML_External_DefaultKind(t *testing.T) {
	t.Parallel()
	raw := `
external:
  label: "No kind specified"
`
	var st spec.Stage
	err := yaml.Unmarshal([]byte(raw), &st)
	require.NoError(t, err)
	require.NotNil(t, st.External)
	require.Equal(t, "subprocess", st.External.Kind, "empty kind should default to subprocess")
}

func TestStage_MarshalUnmarshal_External_Roundtrip(t *testing.T) {
	t.Parallel()
	original := spec.Stage{
		External: &spec.External{
			Label: "Lambda scorer",
			Kind:  "lambda",
			Note:  "AWS Lambda invocation",
		},
	}
	out, err := yaml.Marshal(original)
	require.NoError(t, err)
	var rt spec.Stage
	err = yaml.Unmarshal(out, &rt)
	require.NoError(t, err)
	require.NotNil(t, rt.External)
	require.Equal(t, original.External.Label, rt.External.Label)
	require.Equal(t, original.External.Kind, rt.External.Kind)
	require.Equal(t, original.External.Note, rt.External.Note)
}

func TestLoad_Paper(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "paper.yaml"))
	require.NoError(t, err)
	require.Equal(t, "Paper", s.Title)
	require.Len(t, s.Phases, 6)

	// Phase 4 (index 3) = "Rank & Select" — has a fork stage
	rankPhase := s.Phases[3]
	require.Equal(t, "Rank & Select", rankPhase.Name)
	hasFork := false
	for _, st := range rankPhase.Stages {
		if st.IsFork() {
			hasFork = true
		}
	}
	require.True(t, hasFork, "Rank & Select phase should have a fork stage")

	// Phase 6 (index 5) = "Emit" — has a fanout stage
	emitPhase := s.Phases[5]
	require.Equal(t, "Emit", emitPhase.Name)
	hasFanout := false
	for _, st := range emitPhase.Stages {
		if st.IsFanout() {
			hasFanout = true
		}
	}
	require.True(t, hasFanout, "Emit phase should have a fanout stage")
}

func TestLoad_VybeLoop(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "vybe.yaml"))
	require.NoError(t, err)
	require.Equal(t, "Vybe Loop", s.Title)
	require.True(t, s.HasLoop())
	require.Equal(t, "opts.maxTasks", s.Loop.Max)
	require.Contains(t, s.Loop.ExitConditions, "circuit breaker tripped")

	require.Len(t, s.PrePhases(), 1)
	require.Equal(t, "Boot", s.PrePhases()[0].Name)
	require.Equal(t, spec.PhaseScopeOnceBefore, s.PrePhases()[0].Scope)

	require.Len(t, s.BodyPhases(), 3)
	require.Equal(t, "Claim", s.BodyPhases()[0].Name)
	require.Equal(t, spec.PhaseScopePerIteration, s.BodyPhases()[0].Scope)
	require.Equal(t, "Dispatch", s.BodyPhases()[1].Name)
	require.Equal(t, spec.PhaseScopePerIteration, s.BodyPhases()[1].Scope)
	require.Equal(t, "Resolve", s.BodyPhases()[2].Name)
	require.Equal(t, spec.PhaseScopePerIteration, s.BodyPhases()[2].Scope)

	require.Len(t, s.PostPhases(), 1)
	require.Equal(t, "Record", s.PostPhases()[0].Name)
	require.Equal(t, spec.PhaseScopeOnceAfter, s.PostPhases()[0].Scope)

	require.Equal(t, 1, s.PhaseOrdinal("Boot"))
	require.Equal(t, 2, s.PhaseOrdinal("Claim"))
	require.Equal(t, 5, s.PhaseOrdinal("Record"))
}
