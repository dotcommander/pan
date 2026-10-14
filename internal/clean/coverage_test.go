package clean

import (
	"context"
	"slices"
	"testing"
)

func TestWalkCoverageDepthBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		depth   int
		skipped []string
		files   int
	}{
		{"zero", 0, []string{"SKILL.md", "internal"}, 0},
		{"one", 1, []string{"internal/service.go"}, 2},
		{"full", 2, nil, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeTestFile(t, root, "SKILL.md", "skill")
			writeTestFile(t, root, "internal/service.go", "package service")
			opts := testOptions(root)
			opts.MaxDepth, opts.MaxDepthSet = tc.depth, true
			inventory, err := walkWithCoverage(opts)
			if err != nil {
				t.Fatal(err)
			}
			c := inventory.Coverage
			if c.Complete != (len(tc.skipped) == 0) || !slices.Equal(c.Skipped, tc.skipped) || c.SkippedCount != len(tc.skipped) {
				t.Fatalf("coverage = %+v, want skipped %v", c, tc.skipped)
			}
			if len(inventory.Files) != tc.files {
				t.Fatalf("files = %d, want %d", len(inventory.Files), tc.files)
			}
			if len(tc.skipped) > 0 && !slices.Equal(c.Limits, []string{"max_depth"}) {
				t.Fatalf("limits = %v", c.Limits)
			}
			if len(tc.skipped) == 0 && len(c.Limits) != 0 {
				t.Fatalf("unexpected limits = %v", c.Limits)
			}
		})
	}
}

func TestWalkCoverageDeliberateScopeSkips(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, rel := range []string{".git/config", "excluded/deep/file", ".work/archive/deep/file", "__pycache__/SKILL.md", "nested/.git/config", "nested/code.go"} {
		writeTestFile(t, root, rel, "fixture")
	}
	opts := testOptions(root)
	opts.MaxDepth, opts.MaxDepthSet = 1, true
	opts.Exclude = []string{"excluded", ".work"}
	inventory, err := walkWithCoverage(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Coverage.Complete || inventory.Coverage.SkippedCount != 0 {
		t.Fatalf("deliberate scope skips reported incomplete: %+v", inventory.Coverage)
	}
}

func TestWalkCoverageEmptyAtZero(t *testing.T) {
	t.Parallel()
	opts := testOptions(t.TempDir())
	opts.MaxDepthSet = true
	inventory, err := walkWithCoverage(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Coverage.Complete {
		t.Fatalf("empty walk = %+v", inventory.Coverage)
	}
}

func TestCleanCoverageReports(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("analysis probes local git state")
	}
	root := t.TempDir()
	writeTestFile(t, root, "nested/source.go", "package nested")
	opts := testOptions(root)
	opts.MaxDepth, opts.MaxDepthSet = 1, true
	ctx := context.Background()
	analysis, err := Analyze(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := Findings(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := Missing(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Coverage{analysis.Plan.Coverage, findings.Coverage, missing.Coverage} {
		if c.Complete || !slices.Equal(c.Skipped, []string{"nested/source.go"}) {
			t.Fatalf("report dropped depth coverage: %+v", c)
		}
	}
}
