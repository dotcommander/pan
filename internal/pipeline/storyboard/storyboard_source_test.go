package storyboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestBuildKeepsExplicitScanWarning(t *testing.T) {
	sb := Build(&spec.Spec{
		Title:    "Warnings",
		Coverage: &spec.Coverage{Represented: 1, Total: 3},
	}, "", "scan build failed: partial output")

	if sb.ScanWarning != "scan build failed: partial output" {
		t.Fatalf("ScanWarning = %q, want explicit warning", sb.ScanWarning)
	}
	if sb.Coverage == nil || sb.Coverage.Warning == "" {
		t.Fatalf("coverage warning missing from model: %#v", sb.Coverage)
	}
	got := string(RenderText(sb))
	if !strings.Contains(got, "WARNING: scan build failed: partial output") {
		t.Fatalf("render missing explicit warning:\n%s", got)
	}
	if strings.Contains(got, "WARNING: low scan coverage") {
		t.Fatalf("render promoted coverage warning over explicit scan warning:\n%s", got)
	}
}

func TestProjectNameFallback(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		sourceRoot string
		want       string
	}{
		{
			name:       "title wins",
			title:      "Named",
			sourceRoot: filepath.Join("tmp", "fallback"),
			want:       "Named",
		},
		{
			name:       "source root basename",
			sourceRoot: filepath.Join("tmp", "fallback"),
			want:       "fallback",
		},
		{
			name: "generic fallback",
			want: "Repository",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := projectName(tt.title, tt.sourceRoot); got != tt.want {
				t.Fatalf("projectName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingSourceSnippetDoesNotBlockRendering(t *testing.T) {
	s := &spec.Spec{
		Title: "Missing Source",
		Phases: []spec.Phase{{
			Name: "Run",
			Stages: []spec.Stage{{
				Chip: &spec.Chip{Label: "run", SourceFile: "missing.go", SourceLine: 20},
			}},
		}},
	}

	sb := Build(s, t.TempDir(), "")
	if len(sb.Prelude) != 1 || len(sb.Prelude[0].Stages) != 1 {
		t.Fatalf("unexpected model: %#v", sb)
	}
	stage := sb.Prelude[0].Stages[0]
	if stage.CodeSnippet != "" {
		t.Fatalf("CodeSnippet = %q, want empty", stage.CodeSnippet)
	}
	if got := string(RenderText(sb)); !strings.Contains(got, "run [missing.go:20]") {
		t.Fatalf("render omitted source provenance:\n%s", got)
	}
}

func TestSourceSnippetStaysInsideSourceRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSource(t, parent, "outside.go", []string{
		"package outside",
		`const secret = "outside"`,
	})

	sb := Build(&spec.Spec{
		Title: "Outside",
		Phases: []spec.Phase{{
			Name: "Run",
			Stages: []spec.Stage{{
				Chip: &spec.Chip{Label: "run", SourceFile: "../outside.go", SourceLine: 2},
			}},
		}},
	}, root, "")

	stage := sb.Prelude[0].Stages[0]
	if stage.CodeSnippet != "" {
		t.Fatalf("CodeSnippet = %q, want empty for source outside root", stage.CodeSnippet)
	}
	if got := string(RenderText(sb)); !strings.Contains(got, "run [../outside.go:2]") {
		t.Fatalf("render omitted source provenance:\n%s", got)
	}
}

func TestSourcePathAllowsOnlyRootRelativePaths(t *testing.T) {
	root := filepath.Join("tmp", "repo")
	tests := []struct {
		name       string
		sourceFile string
		wantOK     bool
		wantSuffix string
	}{
		{
			name:       "relative",
			sourceFile: "internal/run.go",
			wantOK:     true,
			wantSuffix: filepath.Join("tmp", "repo", "internal", "run.go"),
		},
		{
			name:       "clean relative",
			sourceFile: "internal/../cmd/run.go",
			wantOK:     true,
			wantSuffix: filepath.Join("tmp", "repo", "cmd", "run.go"),
		},
		{
			name:       "parent traversal",
			sourceFile: "../outside.go",
		},
		{
			name:       "nested parent traversal",
			sourceFile: "internal/../../outside.go",
		},
		{
			name:       "absolute",
			sourceFile: filepath.Join(string(filepath.Separator), "tmp", "outside.go"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sourcePath(root, tt.sourceFile)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (path %q)", ok, tt.wantOK, got)
			}
			if tt.wantOK && got != tt.wantSuffix {
				t.Fatalf("path = %q, want %q", got, tt.wantSuffix)
			}
		})
	}
}

func TestSourceSnippetIsTrimmedAndCapped(t *testing.T) {
	root := t.TempDir()
	longLine := strings.Repeat("x", maxSnippetLen+20)
	writeSource(t, root, "internal/long.go", []string{
		"package internal",
		"   " + longLine + "   ",
	})

	sb := Build(&spec.Spec{
		Title: "Snippet",
		Phases: []spec.Phase{{
			Name: "Run",
			Stages: []spec.Stage{{
				Chip: &spec.Chip{Label: "run", SourceFile: "internal/long.go", SourceLine: 2},
			}},
		}},
	}, root, "")

	got := sb.Prelude[0].Stages[0].CodeSnippet
	if len([]rune(got)) != maxSnippetLen {
		t.Fatalf("snippet length = %d, want %d: %q", len([]rune(got)), maxSnippetLen, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("snippet = %q, want ellipsis suffix", got)
	}
	if strings.Contains(got, " ") {
		t.Fatalf("snippet = %q, want trimmed/capped content without surrounding spaces", got)
	}
}

func writeSource(t *testing.T, root, rel string, lines []string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
