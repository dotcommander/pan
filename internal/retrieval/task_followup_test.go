package retrieval

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestTaskFollowUpCommandHasOnlyArguments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		goal string
		want string
	}{
		{
			name: "spaces",
			goal: "cleanup coverage",
			want: `pan context map --intent "cleanup coverage"`,
		},
		{
			name: "quoted goal",
			goal: `quoted "coverage"`,
			want: `pan context map --intent "quoted \"coverage\""`,
		},
		{
			name: "Markdown symbols",
			goal: "Trace `walkWithCoverage` through `CleanCoverage` to `emitCleanResult`",
			want: "pan context map --intent \"Trace \\`walkWithCoverage\\` through \\`CleanCoverage\\` to \\`emitCleanResult\\`\"",
		},
		{
			name: "shell expansions",
			goal: "$max_depth $(printf changed)",
			want: `pan context map --intent "\$max_depth \$(printf changed)"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report := TaskReport{
				Goal:   tc.goal,
				Budget: TaskBudget{MaxTokens: 4096},
				Truncations: []analyze.Truncation{
					{Field: "targets", Shown: 1, Total: 2, Reason: "target or token budget"},
				},
			}
			if err := finalizeTaskReport(&report); err != nil {
				t.Fatal(err)
			}
			if len(report.FollowUpCommands) != 1 || report.FollowUpCommands[0] != tc.want {
				t.Fatalf("follow-ups = %q, want [%q]", report.FollowUpCommands, tc.want)
			}
		})
	}
}

func TestTaskFollowUpCommandShellRoundTrip(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell probe")
	}
	for _, goal := range []string{
		"Trace `walkWithCoverage` through `CleanCoverage` to `emitCleanResult`",
		"$max_depth $(printf changed)",
		`a\b "quote" 'apostrophe'`,
		"first\nsecond\tline\rend",
		"; printf changed #",
		"résumé coverage",
		"coverage \\",
	} {
		t.Run(goal, func(t *testing.T) {
			t.Parallel()
			report := TaskReport{
				Goal:   goal,
				Budget: TaskBudget{MaxTokens: 4096},
				Truncations: []analyze.Truncation{
					{Field: "targets", Shown: 1, Total: 2, Reason: "target or token budget"},
				},
			}
			if err := finalizeTaskReport(&report); err != nil {
				t.Fatal(err)
			}
			if len(report.FollowUpCommands) != 1 {
				t.Fatalf("follow-ups = %q", report.FollowUpCommands)
			}
			// Only shell built-ins may execute; external substitutions cannot run.
			script := `pan() {
  [ "$#" -eq 4 ] || return 81
  [ "$1" = context ] && [ "$2" = map ] && [ "$3" = --intent ] || return 82
  printf '%s' "$4"
}
` + report.FollowUpCommands[0]
			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", script)
			cmd.Env = []string{"PATH=/nonexistent", "max_depth=expanded"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("shell round trip: %v: %s", err, out)
			}
			if string(out) != goal {
				t.Fatalf("shell intent = %q, want %q", out, goal)
			}
		})
	}
}

func TestTaskFollowUpOmittedWithoutTruncations(t *testing.T) {
	t.Parallel()
	report := TaskReport{
		Goal:             "cleanup coverage",
		Budget:           TaskBudget{MaxTokens: 4096},
		FollowUpCommands: []string{"stale"},
	}
	if err := finalizeTaskReport(&report); err != nil {
		t.Fatal(err)
	}
	if len(report.FollowUpCommands) != 0 {
		t.Fatalf("unneeded follow-ups = %q", report.FollowUpCommands)
	}
}
