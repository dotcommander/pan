package clean

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSafeJoinRejectsNativeAndPortableEscapes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, rel := range []string{".", "a/..", "../outside", `..\outside`, `sub\..\..\outside`, `C:\outside`, `/outside`, `\\host\share`, ".git/config", `sub\.GIT\config`} {
		if _, err := safeJoin(root, rel); err == nil {
			t.Errorf("accepted %q", rel)
		}
	}
	if got, err := safeJoin(root, "sub/file"); err != nil || got != filepath.Join(root, "sub", "file") {
		t.Fatalf("safe path = %q, %v", got, err)
	}
}

func TestTrackedMoveBacksUpBothOperands(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, "old.md", "source")
	writeTestFile(t, root, "docs/new.md", "destination")
	b := newActionBuilder()
	b.gitAction("test", []string{"git", "mv", "--", "old.md", "docs/new.md"})
	if err := validateActions(root, b.actions); err != nil {
		t.Fatal(err)
	}
	rel, count, err := CreateBackup(root, ".work/archive", CollectTargets(b.actions), applyNow())
	if err != nil || count != 2 {
		t.Fatalf("backup = %q %d %v", rel, count, err)
	}
	headers := readBackupHeaders(t, filepath.Join(root, filepath.FromSlash(rel)))
	for _, operand := range []string{"old.md", "docs/new.md"} {
		if headers[operand] == nil {
			t.Fatalf("missing %q", operand)
		}
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v", info.Mode())
	}
}

func TestApplyValidatesGitOperandsBeforeEffects(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	actions := []Action{
		{Kind: KindMkdir, Target: "created"},
		{Kind: KindGit, Argv: []string{"git", "mv", "--", "../secret", "dest"}, Source: "innocent", Target: "dest"},
	}
	if _, err := Apply(context.Background(), testOptions(root), actions, true); err == nil {
		t.Fatal("accepted inconsistent git paths")
	}
	if _, err := os.Stat(filepath.Join(root, "created")); !os.IsNotExist(err) {
		t.Fatalf("directory created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".work")); !os.IsNotExist(err) {
		t.Fatalf("backup effects: %v", err)
	}
}

func TestApplyJoinsActionCancellationAndManifestFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, "blocked", "file")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	manifestFailure := errors.New("manifest fixture failure")
	result, err := applyWithManifest(ctx, testOptions(root), []Action{{Kind: KindMkdir, Target: "blocked"}}, true,
		func(_ Options, _ time.Time, _ string, _ int, _ ApplyResult) (string, error) {
			cancel()
			return ".work/archive/attempt.manifest.json", manifestFailure
		})
	if !errors.Is(err, manifestFailure) || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "create directory") {
		t.Fatalf("joined error = %v", err)
	}
	if result.Backup == "" || result.BackedUp != 1 {
		t.Fatalf("lost backup result: %+v", result)
	}
	if result.Manifest == "" || result.Counts.Failed != 1 || len(result.Actions) != 1 {
		t.Fatalf("lost recovery result: %+v", result)
	}
}

func TestBackupCleansUnpublishedOutputAfterArchiveFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, "present", "data")
	dir, err := prepareArchiveDir(root, ".work/archive")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "failed.tar.gz")
	if _, err := writeBackupArchive(root, dest, []string{"present", "missing"}); err == nil {
		t.Fatal("missing target accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unpublished files retained: %v", entries)
	}
}

func TestExistingTargetsPropagatesNonAbsence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, "file", "data")
	if _, err := existingTargets(root, []string{"file/child"}); err == nil {
		t.Fatal("non-directory stat failure ignored")
	}
}

func TestCleanProcessHelper(t *testing.T) {
	mode := os.Getenv("PAN_CLEAN_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		os.Stdout.WriteString("git version 2.40.0\n")
		os.Exit(0)
	case "overflow":
		buf := make([]byte, maxGitOutputBytes+1024)
		os.Stdout.Write(buf)
		os.Exit(0)
	case "hang":
		<-time.After(10 * time.Minute)
		os.Exit(0)
	}
}

func cleanHelperCmd(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCleanProcessHelper$")
	cmd.Env = append(os.Environ(), "PAN_CLEAN_HELPER="+mode)
	return cmd
}

func TestGitRunExecutionAndCancellation(t *testing.T) {
	t.Parallel()
	ctxSuccess := context.WithValue(context.Background(), gitCommandFactoryKey{}, func(dir string, args ...string) *exec.Cmd {
		return cleanHelperCmd("success")
	})
	out, err := gitRun(ctxSuccess, t.TempDir(), "version")
	if err != nil || !strings.Contains(out, "git version") {
		t.Fatalf("gitRun success: out=%q, err=%v", out, err)
	}

	ctxOverflow := context.WithValue(context.Background(), gitCommandFactoryKey{}, func(dir string, args ...string) *exec.Cmd {
		return cleanHelperCmd("overflow")
	})
	_, err = gitRun(ctxOverflow, t.TempDir(), "ls-files")
	if err == nil || !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("expected output exceeded error, got: %v", err)
	}

	ctxHang := context.WithValue(context.Background(), gitCommandFactoryKey{}, func(dir string, args ...string) *exec.Cmd {
		return cleanHelperCmd("hang")
	})
	canceledCtx, cancel := context.WithCancel(ctxHang)
	cancel()
	if _, err := gitRun(canceledCtx, t.TempDir(), "version"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
