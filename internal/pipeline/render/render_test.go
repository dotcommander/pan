package render_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/pan/internal/pipeline/render"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestRender_Minimal(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "minimal.yaml"))
	require.NoError(t, err)

	outPath := filepath.Join(t.TempDir(), "out", "minimal.html")
	err = render.Render(s, outPath, "")
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	body := string(data)

	require.Contains(t, body, "Minimal")
	require.Contains(t, body, "Only")
	require.Contains(t, body, "in")
	require.Contains(t, body, "out")
}

func TestRender_Paper(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "paper.yaml"))
	require.NoError(t, err)

	outPath := filepath.Join(t.TempDir(), "paper.html")
	err = render.Render(s, outPath, "")
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	body := string(data)

	// All 6 phase names present
	require.Contains(t, body, "Ingest")
	require.Contains(t, body, "Filter")
	require.Contains(t, body, "Dedupe")
	require.Contains(t, body, "Rank &amp; Select")
	require.Contains(t, body, "Polish")
	require.Contains(t, body, "Emit")

	// Fork and fanout blocks present
	require.Contains(t, body, "fork-branches")
	require.Contains(t, body, "fanout")
}

func TestRender_External(t *testing.T) {
	t.Parallel()
	s := &spec.Spec{
		Title: "External Test",
		Phases: []spec.Phase{{
			Name: "Scoring",
			Kind: "Pass",
			Stages: []spec.Stage{
				{Chip: &spec.Chip{Label: "prep"}},
				{External: &spec.External{
					Label: "Python scorer",
					Kind:  "subprocess",
					Note:  "scoring.py",
				}},
			},
		}},
	}
	outPath := filepath.Join(t.TempDir(), "ext.html")
	require.NoError(t, render.Render(s, outPath, ""))
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	body := string(data)
	require.Contains(t, body, "chip--external")
	require.Contains(t, body, "Python scorer")
	require.Contains(t, body, "subprocess")
	require.Contains(t, body, "scoring.py")
}

func TestRender_Dispatcher(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "dispatcher.yaml"))
	require.NoError(t, err)

	outPath := filepath.Join(t.TempDir(), "dispatcher.html")
	err = render.Render(s, outPath, "")
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	body := string(data)

	// Two command lanes must be present (count <section class="command-lane"> tags, not CSS occurrences)
	require.Equal(t, 2, strings.Count(body, `class="command-lane"`), "expected 2 command-lane sections")

	// Both command names must appear
	require.Contains(t, body, ">foo<")
	require.Contains(t, body, ">bar<")

	// Prelude phase (Boot) must also render
	require.Contains(t, body, "Boot")
}

func TestRender_ContainsBaselineFixes(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "paper.yaml"))
	require.NoError(t, err)

	outPath := filepath.Join(t.TempDir(), "fixes.html")
	err = render.Render(s, outPath, "")
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	body := string(data)

	// FIX1: fanout uses responsive grid
	require.Contains(t, body, "repeat(auto-fill, minmax(160px, 1fr))")

	// FIX2: boot chip uses WCAG AA contrast colour
	require.Contains(t, body, "#a8b4c0")

	// FIX3: dead padding-left line from baseline is gone
	require.NotContains(t, body, "padding-left: 48px")

	// FIX4: narrow screens retain a readable phase body and stacked stores.
	require.Contains(t, body, "@media (max-width: 600px)")
	require.Contains(t, body, "grid-template-columns: 56px minmax(0, 1fr)")
	require.Contains(t, body, "flex-direction: column")
}

func TestRender_VybeLoop(t *testing.T) {
	t.Parallel()
	s, err := spec.Load(filepath.Join("..", "..", "..", "testdata", "vybe.yaml"))
	require.NoError(t, err)

	outPath := filepath.Join(t.TempDir(), "vybe.html")
	err = render.Render(s, outPath, "")
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	body := string(data)

	// Loop container and loop badge rendered
	require.Contains(t, body, "loop-container")
	require.Contains(t, body, "loop-badge")
	require.Contains(t, body, "opts.maxTasks")
	require.Contains(t, body, "circuit breaker tripped")

	// Phase scope pills rendered
	require.Contains(t, body, "scope-pill scope-once-before")
	require.Contains(t, body, "scope-pill scope-per-iteration")
	require.Contains(t, body, "scope-pill scope-once-after")

	// All phases present
	require.Contains(t, body, "Boot")
	require.Contains(t, body, "Claim")
	require.Contains(t, body, "Dispatch")
	require.Contains(t, body, "Resolve")
	require.Contains(t, body, "Record")
}
