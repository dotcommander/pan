package clean

import (
	"path/filepath"
	"testing"
)

func TestCleanProtectsSkillInstructions(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{"SKILL.md", "skills/example/SKILL.md"} {
		for _, tracked := range []bool{true, false} {
			name := rel + "/untracked"
			if tracked {
				name = rel + "/tracked"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				opts := testOptions(t.TempDir())
				files := []FileInfo{
					{RelPath: rel, Path: filepath.Join(opts.Root, rel), Tracked: tracked, Size: 50},
					{RelPath: "NOTES.md", Tracked: true, Size: 50},
					{RelPath: "scratch.tmp", Size: 10},
				}
				plan := Categorize(files, opts)
				if len(plan.MisplacedDocs) != 1 || plan.MisplacedDocs[0].File != "NOTES.md" {
					t.Fatalf("misplaced documents = %#v", plan.MisplacedDocs)
				}
				if len(plan.DeleteCandidates) != 1 || plan.DeleteCandidates[0].File != "scratch.tmp" {
					t.Fatalf("delete candidates = %#v", plan.DeleteCandidates)
				}
				for _, action := range BuildActions(plan, opts) {
					if action.Source == rel || action.Target == rel {
						t.Fatalf("protected instruction received an action: %#v", action)
					}
				}
				if plan.Summary["total"] != 2 {
					t.Fatalf("ordinary cleanup changed: %#v", plan.Summary)
				}
			})
		}
	}
}

func TestCleanConfiguredNameProtectionPrecedesMutationRules(t *testing.T) {
	t.Parallel()
	opts := testOptions(t.TempDir())
	opts.Rules.ProtectedNames = []string{"KEEP_PLAN.md", "keep.tmp"}
	plan := Categorize([]FileInfo{
		{RelPath: "KEEP_PLAN.md", Tracked: true, Size: 50},
		{RelPath: "keep.tmp", Size: 10},
		{RelPath: "ordinary.tmp", Size: 10},
	}, opts)
	actions := BuildActions(plan, opts)
	if len(actions) != 1 || actions[0].Target != "ordinary.tmp" || actions[0].Kind != KindRemove {
		t.Fatalf("custom name protection actions = %#v", actions)
	}
	for _, labeled := range plan.AllFiles {
		if labeled.File != "ordinary.tmp" && labeled.Status != StatusClean {
			t.Fatalf("protected file label = %#v", labeled)
		}
	}
}
