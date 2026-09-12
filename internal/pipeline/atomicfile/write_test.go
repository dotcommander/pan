package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesCompleteOutputAndPreservesMode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "spec.yaml")
	if err := Write(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("complete replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "complete replacement" {
		t.Fatalf("output = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("mode = %v, want 0600", gotMode)
	}
	assertNoTemps(t, path)
}

func TestWriteNewDoesNotClobberExistingOutput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, []byte("replacement"), 0o644); err == nil {
		t.Fatal("WriteNew unexpectedly overwrote existing output")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("output = %q, want original", got)
	}
	assertNoTemps(t, path)
}

func TestWriteRejectsDirectory(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	if err := Write(path, []byte("nope"), 0o600); err == nil {
		t.Fatal("Write directory error = nil")
	}
}

func assertNoTemps(t *testing.T, path string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".pan-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary outputs remain: %v", matches)
	}
}
