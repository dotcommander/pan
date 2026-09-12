package storyboard

import (
	"strings"
	"testing"
)

func TestRenderHTMLEmbedsStoryboardBodyAndChrome(t *testing.T) {
	t.Parallel()
	sb := Storyboard{
		ProjectName:  "demo",
		Entrypoint:   "cmd/demo/main.go",
		CommandLanes: []CommandLane{{Name: "build", Phases: []Phase{{Ordinal: 1, Name: "Compile"}}}},
		Coverage:     &Coverage{Represented: 3, Total: 5, Missing: []string{"a.go", "b.go"}},
	}
	out, err := RenderHTML(sb, TextOptions{View: TextViewFull})
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	html := string(out)
	for _, want := range []string{"<!doctype html>", "demo storyboard", "cmd/demo/main.go", "Lifecycle Storyboard", "COMMAND LANES", "build", "<b>3</b>", "<b>5</b>"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	for _, absent := range []string{"Review Findings", "Spec Diff", "Command Drift"} {
		if strings.Contains(html, absent) {
			t.Errorf("HTML unexpectedly contains %q section when data empty", absent)
		}
	}
}

func TestRenderHTMLRendersDriftSectionsWhenPopulated(t *testing.T) {
	t.Parallel()
	sb := Storyboard{
		ProjectName:    "demo",
		ReviewFindings: []ReviewFinding{{Severity: "high", Check: "existence", Message: "missing file"}},
		Diffs:          []DiffItem{{Area: "stores", Status: "added", Detail: "cache"}},
		CommandDrift:   []string{"foo is registered in Cobra but missing from scan/spec command lanes"},
	}
	out, err := RenderHTML(sb, TextOptions{View: TextViewFull})
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	html := string(out)
	for _, want := range []string{"Review Findings", "existence", "missing file", "Spec Diff", "cache", "Command Drift", "registered in Cobra"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}
