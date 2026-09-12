package improve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRefactorStaticcheckPreflightAndChangedPackageGate(t *testing.T) {
	dir := newDeadCodeFixture(t)
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "staticcheck.args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$STATICCHECK_ARGS\"\n"
	path := filepath.Join(bin, "staticcheck")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATICCHECK_ARGS", argsPath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	report, err := RunRefactor(context.Background(), RefactorOptions{RepoPath: dir, StateDir: t.TempDir(), Staticcheck: true})
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(args), ".") || !report.Success || report.Audit == nil {
		t.Fatalf("args=%q report=%#v err=%v", args, report, err)
	}
}

func TestStaticcheckIgnoresUnchangedPackageDiagnostics(t *testing.T) {
	t.Parallel()
	if staticcheckTouchesChangedFile("other.go:2:1: pre-existing", []string{"changed.go"}) {
		t.Fatal("unrelated diagnostic was treated as changed")
	}
}
