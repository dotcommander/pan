package spec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestValidate_OK_Minimal(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Test",
		Phases: []spec.Phase{
			{
				Name: "Only",
				Kind: "Pass",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "in"}},
					{Chip: &spec.Chip{Label: "out"}},
				},
			},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.NoError(t, err)
}

func TestValidate_ErrDuplicatePhase(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{Name: "Ingest", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "a"}}}},
			{Name: "Ingest", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "b"}}}},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), `"Ingest"`)
}

func TestValidate_ErrForkTooFewBranches(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Phase",
				Stages: []spec.Stage{
					{Fork: &spec.Fork{
						Gate:     "Gate",
						Branches: []spec.Branch{{Condition: "yes", Label: "only"}},
					}},
				},
			},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "fork has 1 branches, need >= 2")
}

func TestValidate_ErrFanoutEmptyTargets(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Phase",
				Stages: []spec.Stage{
					{Fanout: &spec.Fanout{Gate: "Dispatch", Targets: []spec.Target{}}},
				},
			},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "fanout has no targets")
}

func TestValidate_MissingFileRef_IsWarnNotError(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name:   "Phase",
				Kind:   "Pass",
				Stages: []spec.Stage{{Chip: &spec.Chip{Label: "a"}}},
				Files:  []string{"does-not-exist.go"},
			},
		},
	}
	// Missing file ref must not be a hard error.
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.NoError(t, err)
}

func TestValidate_FileRef_ResolvesAgainstSpecDir(t *testing.T) {
	t.Parallel()
	// spec.yaml lives in /tmp/test-repoflow/; reference a file that exists there.
	dir := t.TempDir()
	specPath := dir + "/spec.yaml"
	present := dir + "/present.go"
	if f, err := os.Create(present); err == nil {
		_ = f.Close()
	}
	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name:   "Phase",
				Kind:   "Pass",
				Stages: []spec.Stage{{Chip: &spec.Chip{Label: "a"}}},
				Files:  []string{"present.go"},
			},
		},
	}
	// File exists relative to specPath's dir — no warning, no error.
	err := spec.Validate(s, specPath)
	require.NoError(t, err)
}

func TestValidate_ErrEmptyChipLabel(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name: "Phase",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: ""}},
				},
			},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "chip has empty label")
}

func TestValidate_ExternalEmptyLabel_IsWarnNotError(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Phases: []spec.Phase{{
			Name: "Phase",
			Stages: []spec.Stage{
				{External: &spec.External{Label: "", Kind: "subprocess"}},
			},
		}},
	}
	// Empty external label is a warning, not an error.
	require.NoError(t, spec.Validate(s, "/tmp/spec.yaml"))
}

func TestValidate_ExternalValid(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Phases: []spec.Phase{{
			Name:   "Phase",
			Kind:   "Pass",
			Stages: []spec.Stage{{External: &spec.External{Label: "Worker", Kind: "subprocess"}}},
		}},
	}
	require.NoError(t, spec.Validate(s, "/tmp/spec.yaml"))
}

func TestValidate_FileRef_ResolvesFromProjectRoot(t *testing.T) {
	t.Parallel()
	// Layout:
	//   root/go.mod
	//   root/foo.go              <- file ref target
	//   root/sub1/sub2/spec.yaml <- spec lives 2 dirs below root
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "foo.go"), []byte("package test\n"), 0o600))
	subdir := filepath.Join(root, "sub1", "sub2")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	specPath := filepath.Join(subdir, "spec.yaml")

	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name:   "Phase",
				Kind:   "Pass",
				Stages: []spec.Stage{{Chip: &spec.Chip{Label: "a"}}},
				Files:  []string{"foo.go"},
			},
		},
	}
	// foo.go exists at project root (not spec dir) — must resolve correctly.
	err := spec.Validate(s, specPath)
	require.NoError(t, err)
}

func TestProjectRootForSpec_PrefersOverrideThenEmbeddedRoot(t *testing.T) {
	t.Parallel()

	specDir := t.TempDir()
	embedded := filepath.Join(t.TempDir(), "embedded")
	override := filepath.Join(t.TempDir(), "override")
	require.NoError(t, os.MkdirAll(embedded, 0o755))
	require.NoError(t, os.MkdirAll(override, 0o755))

	s := &spec.Spec{Root: embedded}
	specPath := filepath.Join(specDir, "spec.yaml")

	require.Equal(t, embedded, spec.ProjectRootForSpec(s, specPath, ""))
	require.Equal(t, override, spec.ProjectRootForSpec(s, specPath, override))
}

func TestValidate_FileRef_MissingFromProjectRootStillWarnsNotErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600))
	specPath := filepath.Join(root, "spec.yaml")

	s := &spec.Spec{
		Phases: []spec.Phase{
			{
				Name:   "Phase",
				Kind:   "Pass",
				Stages: []spec.Stage{{Chip: &spec.Chip{Label: "a"}}},
				Files:  []string{"nope.go"},
			},
		},
	}
	err := spec.Validate(s, specPath)
	require.NoError(t, err) // missing file ref is warning-only
}

// ─── Commands shape validation ────────────────────────────────────────────────

func TestValidate_CommandsOnly_Valid(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Dispatcher",
		Commands: []spec.Command{
			{
				Name: "foo",
				Phases: []spec.Phase{{
					Name:   "Run",
					Kind:   "Emit",
					Stages: []spec.Stage{{Chip: &spec.Chip{Label: "run"}}},
				}},
			},
		},
	}
	require.NoError(t, spec.Validate(s, "/tmp/spec.yaml"))
}

func TestValidate_PhasesAndCommands_ValidPrelude(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Mixed",
		Phases: []spec.Phase{
			{Name: "Boot", Kind: "Boot", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "main"}}}},
		},
		Commands: []spec.Command{
			{
				Name: "foo",
				Phases: []spec.Phase{{
					Name:   "Run",
					Kind:   "Emit",
					Stages: []spec.Stage{{Chip: &spec.Chip{Label: "run"}}},
				}},
			},
		},
	}
	require.NoError(t, spec.Validate(s, "/tmp/spec.yaml"))
}

func TestValidate_PhasesAndCommands_InvalidPreludeKind(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Mixed",
		Phases: []spec.Phase{
			// Emit kind is not allowed in prelude when commands are present
			{Name: "Emit", Kind: "Emit", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "x"}}}},
		},
		Commands: []spec.Command{
			{
				Name: "foo",
				Phases: []spec.Phase{{
					Name:   "Run",
					Kind:   "Emit",
					Stages: []spec.Stage{{Chip: &spec.Chip{Label: "run"}}},
				}},
			},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot appear in prelude")
}

func TestValidate_PhasesAndCommands_AllowsSharedParseCheckPrelude(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Mixed",
		Phases: []spec.Phase{
			{Name: "Spec", Kind: "Parse", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "load"}}}},
			{Name: "Validate", Kind: "Check", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "validate"}}}},
		},
		Commands: []spec.Command{
			{
				Name: "foo",
				Phases: []spec.Phase{{
					Name:   "Run",
					Kind:   "Emit",
					Stages: []spec.Stage{{Chip: &spec.Chip{Label: "run"}}},
				}},
			},
		},
	}
	require.NoError(t, spec.Validate(s, "/tmp/spec.yaml"))
}

func TestValidate_EmptyCommandPhases_Error(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Dispatcher",
		Commands: []spec.Command{
			{Name: "empty"}, // zero phases
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), `command "empty" has no phases`)
}

func TestValidate_DuplicateCommandName_Error(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Dispatcher",
		Commands: []spec.Command{
			{
				Name:   "foo",
				Phases: []spec.Phase{{Name: "Run", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "x"}}}}},
			},
			{
				Name:   "foo",
				Phases: []spec.Phase{{Name: "Run", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "y"}}}}},
			},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), `duplicate command name "foo"`)
}

func TestValidate_EmptySpec_Error(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{Title: "Empty"}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "spec must have phases or commands")
}

func TestLoad_Dispatcher(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "dispatcher.yaml"))
	require.NoError(t, err)
	require.Equal(t, "Example Dispatcher", s.Title)
	require.Len(t, s.Phases, 1, "prelude has 1 phase (Boot)")
	require.Len(t, s.Commands, 2)
	require.Equal(t, "foo", s.Commands[0].Name)
	require.Equal(t, "bar", s.Commands[1].Name)
	require.Len(t, s.Commands[0].Phases, 1)
	require.Len(t, s.Commands[1].Phases, 1)
}

func TestValidate_VybeLoop_Valid(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "testdata", "vybe.yaml")
	s, err := spec.Load(path)
	require.NoError(t, err)
	err = spec.Validate(s, path)
	require.NoError(t, err)
}

func TestValidate_PhaseScope_Invalid(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "Invalid Scope",
		Phases: []spec.Phase{
			{
				Name:  "Phase1",
				Scope: "not-a-scope",
				Stages: []spec.Stage{
					{Chip: &spec.Chip{Label: "step"}},
				},
			},
		},
	}
	err := spec.Validate(s, "/tmp/spec.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), `has invalid scope "not-a-scope"`)
}

func TestValidate_Loop_Errors(t *testing.T) {
	t.Parallel()

	makeBasePhases := func() []spec.Phase {
		return []spec.Phase{
			{Name: "P1", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "s1"}}}},
			{Name: "P2", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "s2"}}}},
			{Name: "P3", Stages: []spec.Stage{{Chip: &spec.Chip{Label: "s3"}}}},
		}
	}

	t.Run("empty body_phases", func(t *testing.T) {
		t.Parallel()
		s := &spec.Spec{
			Title:  "Test",
			Phases: makeBasePhases(),
			Loop:   &spec.Loop{PrePhases: []string{"P1"}},
		}
		err := spec.Validate(s, "/tmp/spec.yaml")
		require.Error(t, err)
		require.Contains(t, err.Error(), "must define at least one body phase")
	})

	t.Run("unknown phase", func(t *testing.T) {
		t.Parallel()
		s := &spec.Spec{
			Title:  "Test",
			Phases: makeBasePhases(),
			Loop: &spec.Loop{
				BodyPhases: []string{"P1", "NonExistent"},
			},
		}
		err := spec.Validate(s, "/tmp/spec.yaml")
		require.Error(t, err)
		require.Contains(t, err.Error(), `unknown phase "NonExistent"`)
	})

	t.Run("duplicate phase in loop", func(t *testing.T) {
		t.Parallel()
		s := &spec.Spec{
			Title:  "Test",
			Phases: makeBasePhases(),
			Loop: &spec.Loop{
				PrePhases:  []string{"P1"},
				BodyPhases: []string{"P1", "P2"},
			},
		}
		err := spec.Validate(s, "/tmp/spec.yaml")
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot appear in both")
	})

	t.Run("loop does not cover all phases", func(t *testing.T) {
		t.Parallel()
		s := &spec.Spec{
			Title:  "Test",
			Phases: makeBasePhases(),
			Loop: &spec.Loop{
				BodyPhases: []string{"P1", "P2"},
			},
		}
		err := spec.Validate(s, "/tmp/spec.yaml")
		require.Error(t, err)
		require.Contains(t, err.Error(), "loop must cover all phases: phase \"P3\" is not in")
	})

	t.Run("pre_phases order violation", func(t *testing.T) {
		t.Parallel()
		s := &spec.Spec{
			Title:  "Test",
			Phases: makeBasePhases(),
			Loop: &spec.Loop{
				PrePhases:  []string{"P2"},
				BodyPhases: []string{"P1", "P3"},
			},
		}
		err := spec.Validate(s, "/tmp/spec.yaml")
		require.Error(t, err)
		require.Contains(t, err.Error(), "pre_phases must appear before body_phases")
	})

	t.Run("body_phases not contiguous", func(t *testing.T) {
		t.Parallel()
		s := &spec.Spec{
			Title:  "Test",
			Phases: makeBasePhases(),
			Loop: &spec.Loop{
				PrePhases:  []string{"P2"},
				BodyPhases: []string{"P1"},
				PostPhases: []string{"P3"},
			},
		}
		err := spec.Validate(s, "/tmp/spec.yaml")
		require.Error(t, err)
	})

	t.Run("scope mismatch", func(t *testing.T) {
		t.Parallel()
		phases := makeBasePhases()
		phases[0].Scope = spec.PhaseScopeOnceAfter
		s := &spec.Spec{
			Title:  "Test",
			Phases: phases,
			Loop: &spec.Loop{
				PrePhases:  []string{"P1"},
				BodyPhases: []string{"P2", "P3"},
			},
		}
		err := spec.Validate(s, "/tmp/spec.yaml")
		require.Error(t, err)
		require.Contains(t, err.Error(), `has scope "once-after" but loop assigns it "once-before"`)
	})
}
