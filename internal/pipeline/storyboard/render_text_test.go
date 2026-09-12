package storyboard

import (
	"strings"
	"testing"
)

func TestRenderTextCapsCoverageMissingFiles(t *testing.T) {
	sb := Storyboard{
		ProjectName: "Coverage",
		Coverage: &Coverage{
			Represented: 1,
			Total:       8,
			Missing: []string{
				"internal/a.go",
				"internal/b.go",
				"internal/c.go",
				"internal/d.go",
				"internal/e.go",
				"internal/f.go",
				"internal/g.go",
			},
		},
	}

	got := string(RenderText(sb))
	for _, want := range []string{
		"COVERAGE",
		"1/8 source files represented",
		"a.go",
		"e.go",
		"... 2 more",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q\n%s", want, got)
		}
	}
	if strings.Count(got, ".go") != maxCoverageMissingFiles {
		t.Fatalf("rendered missing file count = %d, want %d\n%s", strings.Count(got, ".go"), maxCoverageMissingFiles, got)
	}
	if strings.Contains(got, "f.go") || strings.Contains(got, "g.go") {
		t.Fatalf("render should cap missing files after the configured limit:\n%s", got)
	}
}

func TestRenderTextEmptySections(t *testing.T) {
	got := string(RenderText(Storyboard{ProjectName: "Empty"}))
	for _, want := range []string{
		"STORYBOARD: Empty",
		"SHARED PRELUDE\n└─ none",
		"COMMAND LANES\n└─ no command lanes detected",
		"STORES / STATE\n└─ no stores detected by scan",
		"COVERAGE\n└─ unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q\n%s", want, got)
		}
	}
}

func TestRenderTextGroupsStoreReadersAndWriters(t *testing.T) {
	got := string(RenderText(Storyboard{
		ProjectName: "Stores",
		Stores: []Store{{
			Name: "state.db",
			Writers: []Writer{
				{Stage: "load", Access: "r", Note: "read cache"},
				{Stage: "save", Access: "write", Note: "persist state"},
				{Stage: "touch", Note: "stat"},
			},
		}},
	}))

	for _, want := range []string{
		"state.db",
		"read by: load - read cache",
		"written by: save - persist state",
		"used by: touch - stat",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("store render missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "[write]") || strings.Contains(got, "[r]") {
		t.Fatalf("store render should group access relation instead of raw access badges:\n%s", got)
	}
}

func TestRenderTextLastStageWithSnippetUsesTerminalBranch(t *testing.T) {
	got := string(RenderText(Storyboard{
		ProjectName: "Snippet",
		Prelude: []Phase{{
			Ordinal: 1,
			Name:    "Run",
			Stages: []Stage{{
				Label:       "run",
				CodeSnippet: "func run() {}",
			}},
		}},
	}))
	want := "   └─ stages\n      └─ run() {}"
	if !strings.Contains(got, want) {
		t.Fatalf("rendered stage branch incorrectly, missing %q\n%s", want, got)
	}
	if strings.Contains(got, "   └─ stages\n      ├─ run") {
		t.Fatalf("rendered final stage with non-terminal branch:\n%s", got)
	}
}

func TestRenderTextUsesSharedScanEnginePipeline(t *testing.T) {
	scanPhases := []Phase{
		{
			Ordinal: 1,
			Name:    "Route",
			Kind:    "Route",
			Files:   []string{"internal/scan/detect.go"},
			Stages: []Stage{
				{
					Label:       "addCommandFanout - detects Cobra AddCommand clusters",
					SourceFile:  "internal/scan/detect.go",
					SourceLine:  669,
					CodeSnippet: "func addCommandFanout(_ *token.FileSet, body *ast.BlockStmt) *spec.Fanout {",
				},
				{Label: "fork: fn := call.Fun.(type)"},
				{Label: "*ast.Ident -> return fn.Name"},
				{Label: "*ast.SelectorExpr -> return fn.Sel.Name"},
			},
		},
		{Name: "Kind", Kind: "Analyze", Files: []string{"internal/scan/mapper.go"}},
	}

	sb := Storyboard{
		ProjectName: "Shared",
		CommandLanes: []CommandLane{
			{Name: "map", Phases: append(append([]Phase(nil), scanPhases...), Phase{Name: "Auditpacket"})},
			{Name: "scan", Phases: scanPhases},
			{Name: "storyboard", Phases: append(append([]Phase(nil), scanPhases...), Phase{Name: "Storyboard"})},
		},
	}
	got := string(RenderText(sb))

	for _, want := range []string{
		"SHARED PIPELINES",
		"[Pipeline: Scan Engine]",
		"addCommandFanout(_ *token.FileSet, body *ast.BlockStmt) *spec.Fanout - detects Cobra AddCommand clusters [scan/detect.go:669]",
		"fork: resolve call target",
		"direct identifier -> return fn.Name",
		"selector call -> return fn.Sel.Name",
		"├─ map\n│  ├─ 1. [Pipeline: Scan Engine]\n│  └─ 2. Auditpacket",
		"├─ scan\n│  └─ 1. [Pipeline: Scan Engine]",
		"└─ storyboard\n   ├─ 1. [Pipeline: Scan Engine]\n   └─ 2. Storyboard",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q\n%s", want, got)
		}
	}
	if strings.Count(got, "addCommandFanout") != 1 {
		t.Fatalf("shared scan stage should render once, got %d occurrences\n%s", strings.Count(got, "addCommandFanout"), got)
	}

	focused := string(RenderTextWithOptions(sb, TextOptions{View: "command=storyboard"}))
	for _, want := range []string{
		"COMMAND DETAIL",
		"└─ storyboard\n   ├─ 1. [Pipeline: Scan Engine]\n   │  ├─ 1. Route [Route]",
		"addCommandFanout(_ *token.FileSet, body *ast.BlockStmt) *spec.Fanout",
		"└─ 2. Storyboard",
	} {
		if !strings.Contains(focused, want) {
			t.Fatalf("focused command render missing %q\n%s", want, focused)
		}
	}
}
