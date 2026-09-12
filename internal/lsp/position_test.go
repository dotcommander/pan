package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourcePositionUsesOneBasedLinesAndUTF16Columns(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n// 😀 Target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	line, column, err := SourcePosition(path, 2, "Target")
	if err != nil {
		t.Fatal(err)
	}
	if line != 1 || column != 6 {
		t.Fatalf("position = %d:%d, want 1:6", line, column)
	}
}

func TestSourcePositionRejectsInvalidSelectors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte("value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		line       int
		identifier string
	}{{0, "value"}, {1, "missing"}, {2, "value"}} {
		if _, _, err := SourcePosition(path, test.line, test.identifier); err == nil {
			t.Fatalf("SourcePosition(%d, %q) succeeded", test.line, test.identifier)
		}
	}
}
