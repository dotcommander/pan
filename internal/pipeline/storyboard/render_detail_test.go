package storyboard

import (
	"strings"
	"testing"
)

func TestRenderTextExpandsLongDetailedFileLists(t *testing.T) {
	got := string(RenderText(Storyboard{
		ProjectName: "Details",
		Prelude: []Phase{{
			Name: "Dispatch",
			Files: []string{
				"internal/commands/root.go",
				"internal/commands/init.go",
				"internal/commands/map.go",
				"internal/commands/storyboard.go",
			},
		}},
	}))

	for _, want := range []string{
		"└─ files\n",
		"commands/root.go [command registry]",
		"commands/storyboard.go [command surface]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("detailed render missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "files: commands/root.go") {
		t.Fatalf("detailed render kept long file list inline:\n%s", got)
	}
}

func TestStageLabelTrimsTrailingPunctuationBeforeSource(t *testing.T) {
	got := stageLabel(Stage{
		Label:      "detectDispatchShape - inspects root for a dispatcher-style CLI:",
		SourceFile: "internal/scan/dispatcher.go",
		SourceLine: 38,
	})
	want := "detectDispatchShape - inspects root for a dispatcher-style CLI [scan/dispatcher.go:38]"
	if got != want {
		t.Fatalf("stageLabel() = %q, want %q", got, want)
	}
}

func TestStageLabelCapsLongDescriptions(t *testing.T) {
	got := stageLabel(Stage{
		Label:      strings.Repeat("long ", 40),
		SourceFile: "internal/storyboard/render.go",
		SourceLine: 11,
	})
	if !strings.Contains(got, "… [storyboard/render.go:11]") {
		t.Fatalf("stageLabel() did not cap before source provenance: %q", got)
	}
	if len([]rune(got)) > maxStageLabelLen+len(" [storyboard/render.go:11]") {
		t.Fatalf("stageLabel() length = %d, want capped label: %q", len([]rune(got)), got)
	}
}
