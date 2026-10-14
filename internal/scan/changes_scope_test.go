package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestChangesStayWithinSelectedRoot(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Git history integration")
	}
	root := newDiffTestRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeTree(t, root, map[string]string{
		"selected/inside.go":     "package selected\nfunc Inside() {}\n",
		"selected/deep/inner.go": "package deep\nfunc Inner() {}\n",
		"selected/removed.go":    "package selected\nfunc Removed() {}\n",
	})
	run("add", "--", "selected")
	run("commit", "-q", "-m", "add selected sources")
	writeTree(t, root, map[string]string{
		"base.go":            "package base\nfunc Keep() int { return 2 }\n",
		"selected/inside.go": "package selected\nfunc Inside() int { return 1 }\n",
	})
	if err := os.Remove(filepath.Join(root, "selected/removed.go")); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "base.go", "selected")
	run("commit", "-q", "-m", "fix mixed paths")
	writeTree(t, root, map[string]string{"base.go": "package base\nfunc Keep() int { return 3 }\n"})
	run("add", "--", "base.go")
	run("commit", "-q", "-m", "outside only")
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		dir     string
		top     int
		commits int
		fixes   int
		files   int
		churn   []FileChurn
	}{
		{name: "repository root", commits: 4, fixes: 1, files: 4, churn: []FileChurn{
			{Path: "base.go", Commits: 3, FixTouches: 1},
			{Path: "selected/inside.go", Commits: 2, FixTouches: 1},
			{Path: "selected/removed.go", Commits: 2, FixTouches: 1},
			{Path: "selected/deep/inner.go", Commits: 1},
		}},
		{name: "selected subtree", dir: "selected", commits: 2, fixes: 1, files: 3, churn: []FileChurn{
			{Path: "inside.go", Commits: 2, FixTouches: 1},
			{Path: "removed.go", Commits: 2, FixTouches: 1},
			{Path: "deep/inner.go", Commits: 1},
		}},
		{name: "deeper subtree", dir: "selected/deep", commits: 1, files: 1, churn: []FileChurn{
			{Path: "inner.go", Commits: 1},
		}},
		{name: "empty subtree", dir: "empty", churn: []FileChurn{}},
		{name: "top cap preserves scope counts", dir: "selected", top: 1, commits: 2, fixes: 1, files: 3, churn: []FileChurn{
			{Path: "inside.go", Commits: 2, FixTouches: 1},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report := Changes(t.Context(), filepath.Join(root, tc.dir), 30, tc.top, time.Time{})
			if !report.GitAvailable || report.Note != "" ||
				report.Commits != tc.commits || report.FixCommits != tc.fixes || report.FilesTouched != tc.files {
				t.Fatalf("scope counts = %+v; want commits=%d fixes=%d files=%d", report, tc.commits, tc.fixes, tc.files)
			}
			if !slices.Equal(report.TopFiles, tc.churn) {
				t.Fatalf("churn = %+v, want %+v", report.TopFiles, tc.churn)
			}
			if tc.dir == "selected" {
				if len(report.Recent) != 2 || report.Recent[0].Subject != "fix mixed paths" ||
					report.Recent[0].Files != 2 || report.Recent[1].Files != 3 {
					t.Fatalf("selected recent commits = %+v", report.Recent)
				}
			}
		})
	}
}
