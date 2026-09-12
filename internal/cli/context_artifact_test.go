package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteContextArtifactAtomicallyReplacesRegularFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "result.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeContextArtifact(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("artifact = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteContextArtifactRejectsSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := writeContextArtifact(path, []byte("new")); err == nil {
		t.Fatal("writeContextArtifact succeeded for a symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("target = %q", data)
	}
}

func TestRootArtifactCapturesCommandOutput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "version.txt")
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"--artifact", path, "version"}, Deps{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("pan ")) {
		t.Fatalf("artifact = %q", data)
	}
}
