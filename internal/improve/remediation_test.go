package improve

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ownedprocess"
)

func TestRollbackRemovesAllOwnedTrialArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "tracked.go", "package demo\n")
	writeFile(t, dir, ".gitignore", "ignored/\n")
	baseline, err := InitIsolatedRepository(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := BeginBranchTx(context.Background(), GitVCS{Dir: dir}, baseline, uniqueAttemptBranch("pan-test"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "trial.go", "package demo\n")
	writeFile(t, dir, "ignored/trial.go", "package demo\n")
	if err := tx.RollbackUnlessSettled(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"trial.go", "ignored/trial.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("trial artifact remains: %s: %v", name, err)
		}
	}
}

func TestRollbackRefusesUnownedDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "keep.go", "package keep\n")
	if err := (GitVCS{Dir: dir}).Revert(context.Background(), "HEAD"); err == nil {
		t.Fatal("unowned revert admitted")
	}
	if got := readFile(t, dir, "keep.go"); got != "package keep\n" {
		t.Fatal("unowned source changed")
	}
}

func TestAttemptBranchIdentitiesAreUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for range 100 {
		name := uniqueAttemptBranch("pan-test-r0")
		if seen[name] {
			t.Fatalf("reused branch %s", name)
		}
		seen[name] = true
	}
}

func TestProviderPolicyExcludesBeforeFilesystemOperations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{".work/private.txt", ".claude/private.txt", ".agent-browser-state/state.json", "auth.json", "cookies.json", "src/credentials.json"} {
		writeFile(t, root, name, "PRIVATE_SENTINEL")
	}
	writeFile(t, root, "safe.go", "package safe\n")
	reader, err := newProviderReader(root, nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	// A configured external filesystem backend must not bypass the policy.
	reader.jinnBin = filepath.Join(root, "nonexistent-backend")
	for _, name := range []string{providerListDir, providerSearchFiles, providerFindFiles} {
		result, err := reader.call(context.Background(), name, `{"path":"","pattern":"*"}`)
		if name == providerSearchFiles {
			result, err = reader.call(context.Background(), name, `{"path":"","pattern":"PRIVATE_SENTINEL"}`)
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, "PRIVATE_SENTINEL") || strings.Contains(result, "private.txt") || strings.Contains(result, "cookies") {
			t.Fatalf("denied content exposed: %s", result)
		}
	}
	for _, name := range []string{".work/private.txt", ".claude/private.txt", "auth.json", "cookies.json"} {
		if _, err := reader.readFileRange(name, 0, 0); err == nil {
			t.Fatalf("read admitted: %s", name)
		}
	}
	if err := os.Symlink(filepath.Join(root, ".work/private.txt"), filepath.Join(root, "alias.txt")); err == nil {
		if _, err := reader.readFileRange("alias.txt", 0, 0); err == nil {
			t.Fatal("denied symlink admitted")
		}
	}
}

func TestImproveCaptureUsesConfiguredLimits(t *testing.T) {
	t.Parallel()
	ctx := withOutputLimits(context.Background(), config.ImproveRules{MaxStdoutBytes: 16, MaxStderrBytes: 8})
	cmd := exec.Command(os.Args[0], "-test.run=^TestImproveOutputFixture$")
	cmd.Env = append(os.Environ(), "PAN_IMPROVE_OUTPUT_FIXTURE=stdout")
	if _, err := captureImproveCommand(ctx, cmd); !errors.Is(err, ownedprocess.ErrOutputLimit) {
		t.Fatalf("overflow error = %v", err)
	}
	cmd = exec.Command(os.Args[0], "-test.run=^TestImproveOutputFixture$")
	cmd.Env = append(os.Environ(), "PAN_IMPROVE_OUTPUT_FIXTURE=stderr")
	result, err := captureImproveCommand(ctx, cmd)
	if err != nil || len(result.Stderr) != 8 || !result.StderrTruncated {
		t.Fatalf("stderr result = %+v, %v", result, err)
	}
}

func TestImproveOutputFixture(t *testing.T) {
	t.Parallel()
	mode := os.Getenv("PAN_IMPROVE_OUTPUT_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "stdout" {
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 1024))
	} else {
		_, _ = os.Stderr.WriteString(strings.Repeat("x", 1024))
	}
	os.Exit(0)
}
