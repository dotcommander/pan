package clean

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/config"
)

// applyNow fixes apply timestamps so backup and manifest names are
// deterministic under test.
func applyNow() time.Time {
	return time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
}

func testOptions(dir string) Options {
	return Options{Root: dir, Rules: config.CleanRules{}.Normalized(), Now: applyNow()}
}

func boolp(v bool) *bool { return &v }

func writeTestFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func argvJoined(actions []Action) []string {
	joined := make([]string, 0, len(actions))
	for _, a := range actions {
		joined = append(joined, strings.Join(a.Argv, " "))
	}
	return joined
}

func TestBuildActionsCreatesScriptsDirForReferencedRootScripts(t *testing.T) {
	t.Parallel()

	actions := BuildActions(Plan{
		MisplacedScripts: []Candidate{{
			File:       "deploy.sh",
			Tracked:    boolp(true),
			Referenced: boolp(true),
		}},
	}, testOptions(t.TempDir()))

	if len(actions) != 2 {
		t.Fatalf("len(actions) = %d, want 2: %#v", len(actions), actions)
	}
	if got, want := strings.Join(actions[0].Argv, " "), "mkdir -p -- scripts"; got != want {
		t.Fatalf("first command = %q, want %q", got, want)
	}
	if got, want := strings.Join(actions[1].Argv, " "), "git mv -- deploy.sh scripts/deploy.sh"; got != want {
		t.Fatalf("move command = %q, want %q", got, want)
	}
}

func TestBuildActionsPreservesArchiveRelativePaths(t *testing.T) {
	t.Parallel()

	actions := BuildActions(Plan{
		ArchiveCandidates: []Candidate{
			{File: "tmp/report.txt"},
			{File: "logs/report.txt"},
		},
	}, testOptions(t.TempDir()))

	var moves []string
	for _, a := range actions {
		if a.Kind == KindMove {
			moves = append(moves, strings.Join(a.Argv, " "))
		}
	}
	if len(moves) != 2 {
		t.Fatalf("archive moves = %v, want 2 moves", moves)
	}
	for _, move := range moves {
		if !strings.Contains(move, " .work/archive/2026-01-02/") {
			t.Fatalf("move %q does not target the dated archive root", move)
		}
	}
	for _, suffix := range []string{"/tmp/report.txt", "/logs/report.txt"} {
		found := false
		for _, move := range moves {
			if strings.HasSuffix(move, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no archive move preserves %q in %v", suffix, moves)
		}
	}
	if moves[0] == moves[1] {
		t.Fatalf("archive moves collide: %v", moves)
	}
}

func TestBuildActionsUsesOptionTerminatorsForPathOperands(t *testing.T) {
	t.Parallel()

	actions := BuildActions(Plan{
		DeleteCandidates:  []Candidate{{File: "-scratch.tmp"}},
		ArchiveCandidates: []Candidate{{File: "-notes.md"}},
		UntrackCandidates: []Candidate{{File: "-binary", Tracked: boolp(true)}},
		RenameDocs:        []Candidate{{File: "docs/OLD_NAME.md", Target: "docs/old-name.md", Tracked: boolp(true)}},
	}, testOptions(t.TempDir()))

	joined := argvJoined(actions)
	for _, want := range []string{
		"rm -f -- -scratch.tmp",
		"mv -- -notes.md",
		"git rm --cached -- -binary",
		"git mv -- docs/OLD_NAME.md docs/old-name.md",
	} {
		found := false
		for _, got := range joined {
			if strings.Contains(got, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing command containing %q in %v", want, joined)
		}
	}
}

// TestBuildActionsNeverGitsUntrackedPaths is pan's untracked-never-git
// invariant: untracked candidates only ever receive filesystem moves, and
// every git action addresses a tracked candidate's path.
func TestBuildActionsNeverGitsUntrackedPaths(t *testing.T) {
	t.Parallel()

	untracked := map[string]bool{
		"helper.sh":       true, // referenced, untracked
		"legacy.sh":       true, // unreferenced, untracked
		"docs/OLD_DOC.md": true, // rename candidate, untracked
	}
	actions := BuildActions(Plan{
		MisplacedScripts: []Candidate{
			{File: "deploy.sh", Tracked: boolp(true), Referenced: boolp(true)},
			{File: "helper.sh", Tracked: boolp(false), Referenced: boolp(true)},
			{File: "legacy.sh", Tracked: boolp(false)},
		},
		RenameDocs: []Candidate{
			{File: "docs/OLD_DOC.md", Target: "docs/old-doc.md", Tracked: boolp(false)},
			{File: "docs/OLD_TRACKED.md", Target: "docs/old-tracked.md", Tracked: boolp(true)},
		},
	}, testOptions(t.TempDir()))

	helperMoves := 0
	for _, a := range actions {
		if a.Kind == KindGit && untracked[a.Source] {
			t.Fatalf("git action on untracked path %q: %v", a.Source, a.Argv)
		}
		if a.Kind == KindMove && a.Source == "helper.sh" {
			helperMoves++
			if want := "scripts/helper.sh"; a.Target != want {
				t.Fatalf("helper.sh target = %q, want %q", a.Target, want)
			}
		}
	}
	if helperMoves != 1 {
		t.Fatalf("helper.sh moves = %d, want exactly one filesystem move", helperMoves)
	}
}

// TestApplyDryRunNeverWrites is the dry-run-by-default invariant: without
// --confirm nothing is written — no backup, no manifest, no change.
func TestApplyDryRunNeverWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "scratch.tmp", "scratch")
	writeTestFile(t, dir, "notes.txt", "notes")
	opts := testOptions(dir)

	result, err := Apply(context.Background(), opts, BuildActions(Plan{
		DeleteCandidates:  []Candidate{{File: "scratch.tmp"}},
		ArchiveCandidates: []Candidate{{File: "notes.txt"}},
	}, opts), false)
	if err != nil {
		t.Fatalf("dry-run returned error: %v", err)
	}
	if result.Confirmed {
		t.Fatal("dry-run reported itself as confirmed")
	}
	if result.Backup != "" || result.Manifest != "" || result.BackedUp != 0 {
		t.Fatalf("dry-run created backup artifacts: %+v", result)
	}
	if result.Counts.Executed != 0 || result.Counts.Failed != 0 {
		t.Fatalf("dry-run counts = %+v, want nothing executed", result.Counts)
	}
	for _, path := range []string{"scratch.tmp", "notes.txt"} {
		if _, statErr := os.Lstat(filepath.Join(dir, path)); statErr != nil {
			t.Fatalf("dry-run changed %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Lstat(filepath.Join(dir, ".work")); !os.IsNotExist(statErr) {
		t.Fatalf("dry-run created the archive workspace: %v", statErr)
	}
	for _, a := range result.Actions {
		if a.Status != "planned" {
			t.Fatalf("dry-run action status = %q, want planned", a.Status)
		}
	}
}

// TestApplyBacksUpExistingMoveDestination is the
// backup-before-confirmed-mutation invariant: both the source and the
// existing destination are archived before the move, and the existing
// destination is never overwritten.
func TestApplyBacksUpExistingMoveDestination(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "source.txt", "new")
	writeTestFile(t, dir, "archive/source.txt", "existing")
	opts := testOptions(dir)

	result, err := Apply(context.Background(), opts, []Action{{
		Category: "archive", Kind: KindMove, Source: "source.txt", Target: "archive/source.txt",
	}}, true)
	if err != nil {
		t.Fatalf("confirmed apply failed: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".work", "archive", "pre-cleanup-20260102-150405.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("backup archives = %v, want exactly one", matches)
	}
	entries := readBackupEntries(t, matches[0])
	if got := entries["source.txt"]; got != "new" {
		t.Fatalf("source backup = %q, want new", got)
	}
	if got := entries["archive/source.txt"]; got != "existing" {
		t.Fatalf("destination backup = %q, want existing", got)
	}
	if result.BackedUp != 2 {
		t.Fatalf("backed up %d paths, want 2", result.BackedUp)
	}

	assertMoveOutcome(t, dir)
	assertManifestRecorded(t, dir, result.Manifest, 1)
}

// assertMoveOutcome verifies the existing destination was preserved and
// the source moved beside it; it fails the test directly.
func assertMoveOutcome(t *testing.T, dir string) {
	t.Helper()
	if contents, readErr := os.ReadFile(filepath.Join(dir, "archive", "source.txt")); readErr != nil || string(contents) != "existing" {
		t.Fatalf("existing destination was overwritten: %q, %v", contents, readErr)
	}
	if contents, readErr := os.ReadFile(filepath.Join(dir, "archive", "source-1.txt")); readErr != nil || string(contents) != "new" {
		t.Fatalf("source was not moved beside the destination: %q, %v", contents, readErr)
	}
}

// TestApplyStopsAfterFirstFailureAndKeepsBackup proves the backup is
// written before any mutation: after a failing first action the untouched
// later target is still inside the backup archive, is not executed, and is
// recorded not_run in the manifest.
func TestApplyStopsAfterFirstFailureAndKeepsBackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "sentinel.txt", "keep")
	opts := testOptions(dir)

	result, err := Apply(context.Background(), opts, []Action{
		{Category: "test", Kind: KindMove, Source: "missing.txt", Target: "dest.txt"},
		{Category: "test", Kind: KindRemove, Target: "sentinel.txt"},
	}, true)
	if err == nil {
		t.Fatal("apply returned nil for a failed action")
	}
	if result.Counts.Failed != 1 || result.Counts.NotRun != 1 || result.Counts.Executed != 0 {
		t.Fatalf("counts = %+v, want one failed and one not_run", result.Counts)
	}
	if contents, readErr := os.ReadFile(filepath.Join(dir, "sentinel.txt")); readErr != nil || string(contents) != "keep" {
		t.Fatalf("sentinel changed after failure: %q, %v", contents, readErr)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".work", "archive", "pre-cleanup-20260102-150405.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("backup archives = %v, want exactly one", matches)
	}
	if entries := readBackupEntries(t, matches[0]); entries["sentinel.txt"] != "keep" {
		t.Fatalf("backup entries = %v, want sentinel.txt before any mutation", entries)
	}
	assertManifestRecorded(t, dir, result.Manifest, 2)
}

// TestExecuteFilesystemActionsWithoutPATHTools is the no-shell invariant:
// every filesystem action runs through the Go standard library with no
// PATH tools and no shell, so path names cannot inject commands.
func TestExecuteFilesystemActionsWithoutPATHTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	writeTestFile(t, dir, "source", "data")
	writeTestFile(t, dir, "evil$(touch pwned).txt", "payload")

	ctx := context.Background()
	if err := executeAction(ctx, dir, Action{Kind: KindMkdir, Target: "archive"}); err != nil {
		t.Fatalf("mkdir without PATH tools: %v", err)
	}
	if _, err := executeMove(dir, Action{Kind: KindMove, Source: "source", Target: "archive/moved"}); err != nil {
		t.Fatalf("move without PATH tools: %v", err)
	}
	if _, err := executeMove(dir, Action{Kind: KindMove, Source: "evil$(touch pwned).txt", Target: "archive/evil$(touch pwned).txt"}); err != nil {
		t.Fatalf("move metacharacter path without PATH tools: %v", err)
	}
	if err := executeAction(ctx, dir, Action{Kind: KindRemove, Target: "archive/moved"}); err != nil {
		t.Fatalf("remove without PATH tools: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "moved")); !os.IsNotExist(err) {
		t.Fatalf("moved file still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); !os.IsNotExist(err) {
		t.Fatal("shell metacharacters in a path executed a command")
	}
	if contents, err := os.ReadFile(filepath.Join(dir, "archive", "evil$(touch pwned).txt")); err != nil || string(contents) != "payload" {
		t.Fatalf("metacharacter path not moved intact: %q, %v", contents, err)
	}
}

// TestSafeJoinRejectsUnsafePaths covers the path-safety gate every backup
// and mutation passes through.
func TestSafeJoinRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	root := "/scan/root"
	for _, rel := range []string{"", "..", "../escape", "docs/../../escape", "/etc/passwd", ".git", ".git/config", "docs/.git/hooks"} {
		if got, err := safeJoin(root, rel); err == nil {
			t.Fatalf("safeJoin(root, %q) = %q, want rejection", rel, got)
		}
	}
	got, err := safeJoin(root, "docs/readme.md")
	if err != nil {
		t.Fatalf("safeJoin rejected a safe path: %v", err)
	}
	if want := filepath.Join(root, "docs", "readme.md"); got != want {
		t.Fatalf("safeJoin = %q, want %q", got, want)
	}
}

func TestSafeJoinRejectsSymlinkedDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "archive")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeJoin(root, "archive/moved.txt"); err == nil {
		t.Fatal("safeJoin accepted a symlinked intermediate directory")
	}
}

// readBackupEntries returns the regular-file contents of one backup
// archive keyed by archive path.
func readBackupEntries(t *testing.T, path string) map[string]string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gz.Close() })
	tr := tar.NewReader(gz)
	entries := map[string]string{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		if !header.FileInfo().Mode().IsRegular() {
			continue
		}
		contents, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[header.Name] = string(contents)
	}
}

// assertManifestRecorded verifies the manifest exists beside the backup
// and records every action outcome.
func assertManifestRecorded(t *testing.T, dir, rel string, wantActions int) {
	t.Helper()

	if rel == "" {
		t.Fatal("confirmed apply wrote no manifest path")
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Schema != ManifestSchema {
		t.Fatalf("manifest schema = %q, want %q", manifest.Schema, ManifestSchema)
	}
	if len(manifest.Actions) != wantActions {
		t.Fatalf("manifest actions = %d, want %d", len(manifest.Actions), wantActions)
	}
	if !strings.Contains(filepath.ToSlash(rel), ".work/archive/pre-cleanup-") {
		t.Fatalf("manifest path %q is not beside the backup", rel)
	}
}

// TestApplyCanceledContextPreventsAllMutationsAndArtifacts proves that an
// already-canceled context prevents all backups, manifests, and filesystem mutations.
func TestApplyCanceledContextPreventsAllMutationsAndArtifacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "garbage.tmp", "payload")
	opts := testOptions(dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	actions := []Action{{
		Category: "delete", Kind: KindRemove, Target: "garbage.tmp", Display: "rm -- garbage.tmp",
	}}

	result, err := Apply(ctx, opts, actions, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply with canceled context returned err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "garbage.tmp")); statErr != nil {
		t.Fatalf("target file was modified or deleted despite cancellation: %v", statErr)
	}
	if result.Backup != "" || result.Manifest != "" {
		t.Fatalf("artifacts were created despite cancellation: backup=%q manifest=%q", result.Backup, result.Manifest)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, ".work")); !os.IsNotExist(statErr) {
		t.Fatalf("archive directory was created despite cancellation: %v", statErr)
	}
}

// TestApplyManifestDoesNotOverwriteOnRapidConsecutiveRuns verifies that two
// runs sharing the same timestamp generate distinct manifest paths.
func TestApplyManifestDoesNotOverwriteOnRapidConsecutiveRuns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "file1.tmp", "content1")
	writeTestFile(t, dir, "file2.tmp", "content2")
	opts := testOptions(dir)

	res1, err := Apply(context.Background(), opts, []Action{{
		Category: "delete", Kind: KindRemove, Target: "file1.tmp", Display: "rm -- file1.tmp",
	}}, true)
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}

	res2, err := Apply(context.Background(), opts, []Action{{
		Category: "delete", Kind: KindRemove, Target: "file2.tmp", Display: "rm -- file2.tmp",
	}}, true)
	if err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	if res1.Manifest == "" || res2.Manifest == "" {
		t.Fatalf("expected manifests, got res1=%q res2=%q", res1.Manifest, res2.Manifest)
	}
	if res1.Manifest == res2.Manifest {
		t.Fatalf("manifest paths collided and overwrote: %q == %q", res1.Manifest, res2.Manifest)
	}
	if _, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(res1.Manifest))); statErr != nil {
		t.Fatalf("first manifest missing after second run: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(res2.Manifest))); statErr != nil {
		t.Fatalf("second manifest missing: %v", statErr)
	}
}

type countCanceledContext struct {
	context.Context
	allowedBeforeCancel int
	calls               int
}

func (c *countCanceledContext) Err() error {
	c.calls++
	if c.calls > c.allowedBeforeCancel {
		return context.Canceled
	}
	return nil
}

// TestApplyCancellationMidRunHaltsAndRecordsRemainingNotRun asserts that when
// cancellation occurs after the first action has executed, Apply halts immediately,
// marks remaining actions as not_run, writes the manifest of what was done, and returns context.Canceled.
func TestApplyCancellationMidRunHaltsAndRecordsRemainingNotRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "first.tmp", "content1")
	writeTestFile(t, dir, "second.tmp", "content2")
	opts := testOptions(dir)

	// Call 1: Apply start -> allowed (nil)
	// Call 2: executeAll action 1 -> allowed (nil) -> first.tmp deleted
	// Call 3: executeAll action 2 -> canceled -> second.tmp survives
	ctx := &countCanceledContext{
		Context:             context.Background(),
		allowedBeforeCancel: 2,
	}

	actions := []Action{
		{Category: "delete", Kind: KindRemove, Target: "first.tmp", Display: "rm -- first.tmp"},
		{Category: "delete", Kind: KindRemove, Target: "second.tmp", Display: "rm -- second.tmp"},
	}

	result, err := Apply(ctx, opts, actions, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply returned err = %v, want context.Canceled", err)
	}

	// Action 1 executed
	if _, statErr := os.Lstat(filepath.Join(dir, "first.tmp")); !os.IsNotExist(statErr) {
		t.Fatalf("first.tmp was not removed: %v", statErr)
	}
	// Action 2 was halted and preserved
	if _, statErr := os.Lstat(filepath.Join(dir, "second.tmp")); statErr != nil {
		t.Fatalf("second.tmp was unexpectedly removed: %v", statErr)
	}

	if result.Counts.Executed != 1 {
		t.Fatalf("executed count = %d, want 1", result.Counts.Executed)
	}
	if result.Counts.NotRun != 1 {
		t.Fatalf("not_run count = %d, want 1", result.Counts.NotRun)
	}
	if len(result.Actions) != 2 {
		t.Fatalf("len(result.Actions) = %d, want 2", len(result.Actions))
	}
	if result.Actions[0].Status != "ok" {
		t.Fatalf("first action status = %q, want ok", result.Actions[0].Status)
	}
	if result.Actions[1].Status != "not_run" {
		t.Fatalf("second action status = %q, want not_run", result.Actions[1].Status)
	}
	if result.Manifest == "" {
		t.Fatal("manifest should be recorded for recovery even when canceled mid-run")
	}
	if _, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(result.Manifest))); statErr != nil {
		t.Fatalf("manifest file does not exist on disk: %v", statErr)
	}
}
