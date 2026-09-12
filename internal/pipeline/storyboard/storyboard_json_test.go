package storyboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestBuildAndRenderTextIncludesStoryboardSections(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "internal/run.go", []string{
		"package internal",
		"func Run() {",
		"    println(\"run\")",
		"}",
	})

	s := &spec.Spec{
		Title:      "Example",
		Breadcrumb: "cmd/example -> internal/run.go",
		Phases: []spec.Phase{{
			Name:           "Boot",
			Kind:           spec.PhaseKindBoot,
			Description:    "prepare command",
			Files:          []string{"cmd/example/main.go"},
			TruncatedCount: 2,
			Stages: []spec.Stage{{
				Chip: &spec.Chip{
					Label:      "main",
					SourceFile: "internal/run.go",
					SourceLine: 3,
				},
			}},
		}},
		Commands: []spec.Command{{
			Name:        "serve",
			Description: "run server",
			Phases: []spec.Phase{{
				Name: "Serve",
				Kind: spec.PhaseKindServe,
				Stages: []spec.Stage{{
					Fork: &spec.Fork{
						Gate: "mode",
						Branches: []spec.Branch{{
							Condition:  "watch",
							Label:      "reload",
							SourceFile: "internal/run.go",
							SourceLine: 2,
						}},
					},
				}},
			}},
		}},
		Stores: []spec.Store{{
			Name: "repoflow.yaml",
			Writers: []spec.Writer{{
				Stage:  "scan",
				Access: "write",
				Note:   "generated spec",
			}},
		}},
		Coverage: &spec.Coverage{Represented: 1, Total: 2, Missing: []string{"internal/missing.go"}},
	}

	sb := Build(s, root, "")
	got := string(RenderText(sb))
	for _, want := range []string{
		"STORYBOARD: Example",
		"ENTRYPOINT: cmd/example -> internal/run.go",
		"WARNING: low scan coverage: 1/2 source files represented",
		"SHARED PRELUDE",
		"Boot [Boot] - prepare command (+2 stages truncated)",
		"main [run.go:3]",
		"COMMAND LANES",
		"serve - run server",
		"fork: mode",
		"watch -> reload [run.go:2]",
		"STORES / STATE",
		"repoflow.yaml",
		"written by: scan - generated spec",
		"COVERAGE",
		"missing.go",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q\n%s", want, got)
		}
	}
}

func TestRenderJSONIsStableAndSemantic(t *testing.T) {
	t.Parallel()
	decoded, got := renderedJSONStoryboard(t)
	t.Run("sorts command lanes", func(t *testing.T) {
		assertCommandLaneOrder(t, got)
	})
	t.Run("preserves phase provenance", func(t *testing.T) {
		assertJSONPhase(t, decoded)
	})
	t.Run("preserves stores and coverage", func(t *testing.T) {
		assertJSONStoresAndCoverage(t, decoded)
	})
}

func renderedJSONStoryboard(t *testing.T) (Storyboard, string) {
	t.Helper()
	root := t.TempDir()
	writeSource(t, root, "internal/run.go", []string{
		"package internal",
		"func Run() {}",
	})
	s := &spec.Spec{
		Title: "Example",
		Phases: []spec.Phase{{
			Name:           "Boot",
			TruncatedCount: 4,
			Stages: []spec.Stage{{
				Chip: &spec.Chip{Label: "run", SourceFile: "internal/run.go", SourceLine: 2},
			}},
		}},
		Commands: []spec.Command{
			{Name: "zeta", Phases: []spec.Phase{{Name: "Z"}}},
			{Name: "alpha", Phases: []spec.Phase{{Name: "A"}}},
		},
		Stores: []spec.Store{{
			Name: "state.db",
			Writers: []spec.Writer{{
				Stage:  "run",
				Access: "write",
				Note:   "persist state",
			}},
		}},
		Coverage: &spec.Coverage{Represented: 3, Total: 3},
	}

	data, err := RenderJSON(Build(s, root, ""))
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("invalid JSON: %s", data)
	}
	var decoded Storyboard
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	return decoded, string(data)
}

func assertCommandLaneOrder(t *testing.T, got string) {
	t.Helper()
	alpha := strings.Index(got, `"name": "alpha"`)
	zeta := strings.Index(got, `"name": "zeta"`)
	if alpha < 0 || zeta < 0 || alpha > zeta {
		t.Fatalf("command lanes not sorted by name:\n%s", got)
	}
}

func assertJSONPhase(t *testing.T, decoded Storyboard) {
	t.Helper()
	if decoded.ProjectName != "Example" {
		t.Fatalf("ProjectName = %q, want Example", decoded.ProjectName)
	}
	if len(decoded.Prelude) != 1 {
		t.Fatalf("Prelude length = %d, want 1", len(decoded.Prelude))
	}
	phase := decoded.Prelude[0]
	if phase.TruncatedCount != 4 {
		t.Fatalf("TruncatedCount = %d, want 4", phase.TruncatedCount)
	}
	if len(phase.Stages) != 1 {
		t.Fatalf("stage length = %d, want 1", len(phase.Stages))
	}
	stage := phase.Stages[0]
	if stage.SourceFile != "internal/run.go" || stage.SourceLine != 2 || stage.CodeSnippet != "func Run() {}" {
		t.Fatalf("stage provenance = %#v", stage)
	}
}

func assertJSONStoresAndCoverage(t *testing.T, decoded Storyboard) {
	t.Helper()
	if len(decoded.CommandLanes) != 2 {
		t.Fatalf("CommandLanes length = %d, want 2", len(decoded.CommandLanes))
	}
	if len(decoded.Stores) != 1 || decoded.Stores[0].Name != "state.db" {
		t.Fatalf("Stores = %#v, want state.db", decoded.Stores)
	}
	if len(decoded.Stores[0].Writers) != 1 {
		t.Fatalf("Writers = %#v, want one writer", decoded.Stores[0].Writers)
	}
	writer := decoded.Stores[0].Writers[0]
	if writer.Stage != "run" || writer.Access != "write" || writer.Note != "persist state" {
		t.Fatalf("writer = %#v", writer)
	}
	if decoded.Coverage == nil || decoded.Coverage.Represented != 3 || decoded.Coverage.Total != 3 {
		t.Fatalf("Coverage = %#v, want 3/3", decoded.Coverage)
	}
}
