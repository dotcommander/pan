package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestContextImpactEmitsFileEvidence(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	args := []string{"--repo", basicGoRepo(), "--format", "json", "context", "impact", "internal/service/service.go"}
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"command": [`, `"impact"`, `"parse_method": "go_ast"`, `"score_components"`, `"exported_symbols"`} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestContextImpactAcceptsAbsolutePathAndRejectsEscape(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(basicGoRepo())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", root, "context", "impact", filepath.Join(root, "internal/service/service.go")}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(context.Background(), []string{"--repo", root, "context", "impact", filepath.Join(root, "..", "outside.go")}, newTestDeps(&bytes.Buffer{})); err == nil {
		t.Fatal("outside impact target must fail")
	}
}
