package bench

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

// gitFixture builds one git mirror with two commits and returns the mirror
// path and the second commit hash.
func gitFixture(t *testing.T, files map[string]string) (mirror string, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	mirror = filepath.Join(dir, "acme__widget")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = mirror
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=pan", "GIT_AUTHOR_EMAIL=pan@example.com",
			"GIT_COMMITTER_NAME=pan", "GIT_COMMITTER_EMAIL=pan@example.com",
		)
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out.String())
		}
		return strings.TrimSpace(out.String())
	}
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	for path, source := range files {
		full := filepath.Join(mirror, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "fixture")
	commit = run("rev-parse", "HEAD")
	return mirror, commit
}

// snapshotSource stamps deterministic identity on real analyze.Build results.
type snapshotSource struct{}

func (snapshotSource) Snapshot(ctx context.Context, root string) (analyze.Snapshot, error) {
	snap, err := analyze.Build(ctx, root, config.Config{
		MaxFiles: 100, MaxFileBytes: 64 << 10, MaxTotalBytes: 1 << 20,
		MaxNodes: 500, OutputBudget: 8 << 10, CommandTimeout: 5 * time.Second,
	})
	if err != nil {
		return snap, err
	}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: "fixture-snapshot", Source: "live"}
	return snap, nil
}

func TestRunScoresInstanceEndToEnd(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"widget.go":    "package widget\n\nimport \"fmt\"\n\n// ParseWidget parses the widget input text.\nfunc ParseWidget(input string) (string, error) {\n\treturn fmt.Sprintf(\"%s!\", input), nil\n}\n",
		"unrelated.go": "package widget\n\nfunc Unrelated() int { return 1 }\n",
		"README.md":    "# widget\n\nA tiny fixture repository.\n",
	}
	mirror, commit := gitFixture(t, files)

	dataset := writeInstances(t, t.TempDir(), []map[string]any{
		{
			"instance_id":       "acme__widget-1",
			"repo":              "acme/widget",
			"base_commit":       commit,
			"problem_statement": "ParseWidget should parse the widget input; adjust the parser.",
			"patch":             "--- a/widget.go\n+++ b/widget.go\n@@ -1 +1 @@\n-x\n+y\n",
		},
	})
	mirrors := filepath.Dir(mirror)
	work := t.TempDir()
	options := RunOptions{Dataset: dataset, Mirrors: mirrors, Work: work, TokenBudget: 512, TopRows: 10}
	report, err := Run(context.Background(), snapshotSource{}, options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Schema != ReportSchema {
		t.Fatalf("schema = %q", report.Schema)
	}
	if report.Instances != 1 || report.Scored != 1 || len(report.Skipped) != 0 {
		t.Fatalf("instances=%d scored=%d skipped=%+v", report.Instances, report.Scored, report.Skipped)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d", len(report.Rows))
	}
	row := report.Rows[0]
	if !slices.Equal(row.GoldPaths, []string{"widget.go"}) {
		t.Fatalf("gold = %v", row.GoldPaths)
	}
	if !slices.Contains(row.PanPaths, "widget.go") {
		t.Fatalf("pan ranking %v must include widget.go", row.PanPaths)
	}
	if !slices.Contains(row.BM25Paths, "widget.go") {
		t.Fatalf("bm25 ranking %v must include widget.go", row.BM25Paths)
	}
	if len(report.Systems) != 2 || report.Systems[0].System != SystemPan || report.Systems[1].System != SystemBM25 {
		t.Fatalf("systems = %+v", report.Systems)
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 0 {
		t.Fatalf("work dir must be cleaned after scoring, found %v (err=%v)", entries, err)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"parser.go":   "package widget\n\n// Parse parses input.\nfunc Parse(input string) error { return nil }\n",
		"consumer.go": "package widget\n\nfunc Use() { _ = Parse(\"x\") }\n",
	}
	mirror, commit := gitFixture(t, files)
	dataset := writeInstances(t, t.TempDir(), []map[string]any{
		{
			"instance_id":       "acme__widget-2",
			"repo":              "acme/widget",
			"base_commit":       commit,
			"problem_statement": "Parse fails on empty input.",
			"patch":             "--- a/parser.go\n+++ b/parser.go\n",
		},
	})
	options := RunOptions{
		Dataset: dataset, Mirrors: filepath.Dir(mirror), Work: t.TempDir(),
		TokenBudget: 512, TopRows: 10,
	}
	first, err := Run(context.Background(), snapshotSource{}, options)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := Run(context.Background(), snapshotSource{}, options)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("repeat runs differ:\n%s\n%s", firstJSON, secondJSON)
	}
}

func TestRunSkipsMissingMirrorAndBadCommit(t *testing.T) {
	t.Parallel()
	dataset := writeInstances(t, t.TempDir(), []map[string]any{
		{
			"instance_id":       "acme__widget-3",
			"repo":              "acme/widget",
			"base_commit":       "0123456789abcdef0123456789abcdef01234567",
			"problem_statement": "Anything.",
			"patch":             "--- a/widget.go\n+++ b/widget.go\n",
		},
	})
	// Mirror directory exists but the commit does not.
	mirrors := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mirrors, "acme__widget"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	report, err := Run(context.Background(), snapshotSource{}, RunOptions{
		Dataset: dataset, Mirrors: mirrors, Work: t.TempDir(), TokenBudget: 256,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Scored != 0 || len(report.Skipped) != 1 {
		t.Fatalf("scored=%d skipped=%+v", report.Scored, report.Skipped)
	}
	if !strings.Contains(report.Skipped[0].Reason, "checkout failed") {
		t.Fatalf("skip reason = %q", report.Skipped[0].Reason)
	}
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	if _, err := Run(context.Background(), snapshotSource{}, RunOptions{}); err == nil {
		t.Fatal("Run without dataset must fail")
	}
	if _, err := Run(context.Background(), nil, RunOptions{Dataset: "x"}); err == nil {
		t.Fatal("Run without snapshot source must fail")
	}
}

func TestArchiveTargetGuards(t *testing.T) {
	t.Parallel()
	if _, ok := archiveTarget("/work", "pkg/file.go"); !ok {
		t.Fatal("normal entry must be accepted")
	}
	for _, name := range []string{"/abs.go", "../escape.go", "a/../../escape.go", ""} {
		if _, ok := archiveTarget("/work", name); ok {
			t.Fatalf("archiveTarget(%q) accepted, want rejected", name)
		}
	}
}
