package storyboard

import (
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestBuildTrimsChipSubtitleBeforeComposingStageLabel(t *testing.T) {
	sb := Build(&spec.Spec{
		Title: "Subtitle",
		Phases: []spec.Phase{{
			Name: "Run",
			Stages: []spec.Stage{{
				Chip: &spec.Chip{Label: "NewRoot", Subtitle: "  → *cobra.Command  "},
			}},
		}},
	}, "", "")

	if got := sb.Prelude[0].Stages[0].Label; got != "NewRoot - → *cobra.Command" {
		t.Fatalf("stage label = %q, want trimmed subtitle", got)
	}
	if got := string(RenderText(sb)); strings.Contains(got, " -  ") {
		t.Fatalf("render contains doubled separator spacing:\n%s", got)
	}
}

func TestBuildJoinsOptionalBranchAndTargetLabelParts(t *testing.T) {
	sb := Build(&spec.Spec{
		Title: "Parts",
		Phases: []spec.Phase{{
			Name: "Dispatch",
			Stages: []spec.Stage{
				{Fork: &spec.Fork{
					Gate: "mode",
					Branches: []spec.Branch{
						{Condition: "fast", Label: "quick path"},
						{Condition: "", Label: "fallback"},
					},
				}},
				{Fanout: &spec.Fanout{
					Gate: "targets",
					Targets: []spec.Target{
						{Flag: "--file", Label: "output file"},
						{Flag: "  ", Label: "stdout"},
					},
				}},
			},
		}},
	}, "", "")

	got := []string{}
	for _, stage := range sb.Prelude[0].Stages {
		got = append(got, stage.Label)
	}
	want := []string{
		"fork: mode",
		"fast -> quick path",
		"fallback",
		"fanout: targets",
		"--file -> output file",
		"stdout",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stage labels = %#v, want %#v", got, want)
	}
	if rendered := string(RenderText(sb)); strings.Contains(rendered, "─  -> ") {
		t.Fatalf("render contains dangling optional label arrow:\n%s", rendered)
	}
}

func TestBuildIncludesExternalSourceProvenanceAndSnippet(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "internal/external.go", []string{
		"package internal",
		"func RunExternal() {}",
	})

	sb := Build(&spec.Spec{
		Title: "External",
		Phases: []spec.Phase{{
			Name: "Callout",
			Stages: []spec.Stage{{
				External: &spec.External{
					Label:      "Python scorer",
					Kind:       "subprocess",
					SourceFile: "internal/external.go",
					SourceLine: 2,
				},
			}},
		}},
	}, root, "")

	stage := sb.Prelude[0].Stages[0]
	if stage.Label != "external: Python scorer (subprocess)" {
		t.Fatalf("external label = %q, want kind-qualified label", stage.Label)
	}
	if stage.Role != "io" {
		t.Fatalf("external role = %q, want io", stage.Role)
	}
	if stage.SourceFile != "internal/external.go" || stage.SourceLine != 2 || stage.CodeSnippet != "func RunExternal() {}" {
		t.Fatalf("external source provenance = %#v", stage)
	}
	got := string(RenderText(sb))
	for _, want := range []string{
		"io: external: Python scorer (subprocess) [external.go:2]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q\n%s", want, got)
		}
	}
}

func TestBuildInfersStageRoles(t *testing.T) {
	sb := Build(&spec.Spec{
		Title: "Roles",
		Phases: []spec.Phase{{
			Name: "Boot",
			Stages: []spec.Stage{
				{Chip: &spec.Chip{Label: "main", SourceFile: "cmd/demo/main.go", SourceLine: 1}},
				{Chip: &spec.Chip{Label: "newRunCmd", SourceFile: "internal/commands/run.go", SourceLine: 2}},
				{Chip: &spec.Chip{Label: "Load", SourceFile: "internal/spec/load.go", SourceLine: 3}},
				{Chip: &spec.Chip{Label: "Scan", SourceFile: "internal/scan/scan.go", SourceLine: 4}},
				{Chip: &spec.Chip{Label: "Render", SourceFile: "internal/render/render.go", SourceLine: 5}},
				{Chip: &spec.Chip{Label: "watchLoop", SourceFile: "internal/serve/watcher.go", SourceLine: 6}},
			},
		}},
	}, "", "")

	got := []string{}
	for _, stage := range sb.Prelude[0].Stages {
		got = append(got, stage.Role)
	}
	want := []string{"entry", "route", "parse", "analyze", "render", "watch"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %#v, want %#v", got, want)
	}

	data, err := RenderJSON(sb)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !strings.Contains(string(data), `"role": "entry"`) || !strings.Contains(string(data), `"role": "watch"`) {
		t.Fatalf("json missing inferred roles:\n%s", data)
	}
}

func TestJoinLabelParts(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  string
	}{
		{name: "both", left: "a", right: "b", want: "a -> b"},
		{name: "left only", left: "a", want: "a"},
		{name: "right only", right: "b", want: "b"},
		{name: "trims", left: " a ", right: " b ", want: "a -> b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinLabelParts(tt.left, tt.right); got != tt.want {
				t.Fatalf("joinLabelParts() = %q, want %q", got, tt.want)
			}
		})
	}
}
