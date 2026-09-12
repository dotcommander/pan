package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/lsp"
)

func TestLSPPositionArgsAcceptSourceIdentifier(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n// Target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, err := (lspPositionArgs{File: "sample.go", Line: 2, Selector: "Target"}).options(root, lsp.CapabilityDef)
	if err != nil {
		t.Fatal(err)
	}
	if opts.File != path || opts.Line != 1 || opts.Column != 3 || opts.Capability != lsp.CapabilityDef {
		t.Fatalf("options = %#v", opts)
	}
}

func TestLSPPositionArgsRetainsNumericColumn(t *testing.T) {
	t.Parallel()
	opts, err := (lspPositionArgs{File: "sample.go", Line: 0, Selector: "4"}).options(".", lsp.CapabilityHover)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Line != 0 || opts.Column != 4 {
		t.Fatalf("options = %#v", opts)
	}
}
