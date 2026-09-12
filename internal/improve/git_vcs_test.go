package improve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGitVCSRollsBackDisposableBranch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "demo.go", "package demo\n")
	baseline, err := InitIsolatedRepository(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := BeginBranchTx(context.Background(), GitVCS{Dir: dir}, baseline, "pan-improve-attempt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.go"), []byte("package demo\n\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tx.RollbackUnlessSettled(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir, "demo.go"); got != "package demo\n" {
		t.Fatalf("rollback contents = %q", got)
	}
	if _, err := gitOutput(context.Background(), dir, "rev-parse", "--verify", "pan-improve-attempt"); err == nil {
		t.Fatal("attempt branch remains after rollback")
	}
}
