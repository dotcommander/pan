package storyboard

import (
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestBuildAnnotatesSharedPipelinesForJSON(t *testing.T) {
	scanPhases := []spec.Phase{
		{Name: "Dispatch", Files: []string{"internal/commands/scan.go", "internal/scan/detect.go"}},
		{Name: "Scan", Files: []string{"internal/scan/dispatcher.go", "internal/scan/fill.go"}},
		{Name: "Kind", Files: []string{"internal/scan/mapper.go"}},
	}
	consumerPhases := []spec.Phase{
		{Name: "Route", Files: []string{"internal/commands/storyboard.go", "internal/scan/dispatcher.go", "internal/scan/fill.go"}},
		{Name: "Kind", Files: []string{"internal/scan/mapper.go"}},
	}
	sb := Build(&spec.Spec{
		Title: "Shared",
		Commands: []spec.Command{
			{Name: "map", Phases: append(append([]spec.Phase(nil), consumerPhases...), spec.Phase{Name: "Auditpacket"})},
			{Name: "scan", Phases: scanPhases},
			{Name: "storyboard", Phases: append(append([]spec.Phase(nil), consumerPhases...), spec.Phase{Name: "Storyboard"})},
		},
	}, "", "")

	if len(sb.SharedPipelines) != 1 {
		t.Fatalf("SharedPipelines length = %d, want 1: %#v", len(sb.SharedPipelines), sb.SharedPipelines)
	}
	pipeline := sb.SharedPipelines[0]
	if pipeline.Name != scanEnginePipelineName {
		t.Fatalf("pipeline name = %q, want %q", pipeline.Name, scanEnginePipelineName)
	}
	if len(pipeline.Phases) != 2 {
		t.Fatalf("pipeline phases = %d, want 2: %#v", len(pipeline.Phases), pipeline.Phases)
	}
	if len(pipeline.Lanes) != 3 {
		t.Fatalf("pipeline lanes = %d, want 3: %#v", len(pipeline.Lanes), pipeline.Lanes)
	}
	for _, lane := range sb.CommandLanes {
		if len(lane.PipelineRefs) != 1 {
			t.Fatalf("lane %s PipelineRefs = %#v, want one", lane.Name, lane.PipelineRefs)
		}
		if lane.PipelineRefs[0].Name != scanEnginePipelineName {
			t.Fatalf("lane %s pipeline ref = %#v", lane.Name, lane.PipelineRefs[0])
		}
		if lane.PipelineRefs[0].PhaseCount == 0 {
			t.Fatalf("lane %s pipeline ref has zero phase count: %#v", lane.Name, lane.PipelineRefs[0])
		}
	}

	data, err := RenderJSON(sb)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		`"shared_pipelines"`,
		`"pipeline_refs"`,
		`"name": "Scan Engine"`,
		`"phase_count": 3`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("json missing %q\n%s", want, got)
		}
	}
}

func TestRenderTextSummaryKeepsDetailsCollapsed(t *testing.T) {
	sb := Storyboard{
		ProjectName: "Summary",
		Prelude: []Phase{{
			Name:  "Boot",
			Kind:  "Boot",
			Files: []string{"cmd/demo/main.go"},
			Stages: []Stage{{
				Label:       "main",
				SourceFile:  "cmd/demo/main.go",
				SourceLine:  8,
				CodeSnippet: "func main() {",
			}},
		}},
		CommandLanes: []CommandLane{{
			Name: "run",
			Phases: []Phase{{
				Name:  "Run",
				Kind:  "Route",
				Files: []string{"internal/commands/run.go"},
				Stages: []Stage{{
					Label:      "run",
					SourceFile: "internal/commands/run.go",
					SourceLine: 12,
				}},
			}},
		}},
	}

	got := string(RenderTextWithOptions(sb, TextOptions{View: TextViewSummary}))
	for _, want := range []string{
		"SHARED PRELUDE",
		"1. Boot [Boot] | cmd/demo/main.go [entrypoint]",
		"COMMAND LANES",
		"run",
		"1. Run [Route] | commands/run.go [command surface]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary render missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "stages\n") || strings.Contains(got, "main()") || strings.Contains(got, "run [commands/run.go:12]") {
		t.Fatalf("summary view expanded implementation detail:\n%s", got)
	}
}

func TestRenderTextCommandViewExpandsOnlySelectedLane(t *testing.T) {
	sb := Storyboard{
		ProjectName: "Focused",
		CommandLanes: []CommandLane{
			{
				Name: "alpha",
				Phases: []Phase{{
					Name:  "Alpha",
					Files: []string{"internal/commands/alpha.go"},
					Stages: []Stage{{
						Label:      "alpha",
						SourceFile: "internal/commands/alpha.go",
						SourceLine: 7,
					}},
				}},
			},
			{
				Name: "beta",
				Phases: []Phase{{
					Name:  "Beta",
					Files: []string{"internal/commands/beta.go"},
					Stages: []Stage{{
						Label:      "beta",
						SourceFile: "internal/commands/beta.go",
						SourceLine: 9,
					}},
				}},
			},
		},
	}

	got := string(RenderTextWithOptions(sb, TextOptions{View: "command=beta"}))
	for _, want := range []string{
		"alpha",
		"beta *",
		"COMMAND DETAIL",
		"└─ beta",
		"beta [commands/beta.go:9]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("command render missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "alpha [commands/alpha.go:7]") {
		t.Fatalf("command view expanded unselected lane:\n%s", got)
	}
}

func TestStageLabelFormatsSourceProvenance(t *testing.T) {
	tests := []struct {
		name  string
		stage Stage
		want  string
	}{
		{
			name:  "label only",
			stage: Stage{Label: "run"},
			want:  "run",
		},
		{
			name:  "file only",
			stage: Stage{Label: "run", SourceFile: "internal/run.go"},
			want:  "run [run.go]",
		},
		{
			name:  "file and line",
			stage: Stage{Label: "run", SourceFile: "internal/run.go", SourceLine: 12},
			want:  "run [run.go:12]",
		},
		{
			name: "function signature",
			stage: Stage{
				Label:       "Run - executes work",
				SourceFile:  "internal/run.go",
				SourceLine:  12,
				CodeSnippet: "func Run(ctx context.Context) error {",
			},
			want: "Run(ctx context.Context) error - executes work [run.go:12]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stageLabel(tt.stage); got != tt.want {
				t.Fatalf("stageLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
