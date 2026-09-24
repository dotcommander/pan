package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestFlowCallsRequiresHandleForCollidingNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for file, source := range map[string]string{
		"go.mod": "module example.test/flowcalls\n\ngo 1.25\n",
		"a/a.go": "package a\nfunc Run() {}\n",
		"b/b.go": "package b\nfunc Run() {}\n",
		"use.go": "package flowcalls\nimport (\"example.test/flowcalls/a\"; \"example.test/flowcalls/b\")\nfunc Use() { a.Run(); b.Run() }\n",
	} {
		filename := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--repo", root, "--format", "json", "flow", "calls", "Run"}
	err := cli.Run(context.Background(), args, newTestDeps(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "ambiguous symbol") {
		t.Fatalf("flow calls Run error = %v, want alternatives", err)
	}
	var out bytes.Buffer
	args[len(args)-1] = "symbol:a/a.go::Run#function@2"
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"path": "a/a.go"`)) || bytes.Contains(out.Bytes(), []byte(`"path": "b/b.go"`)) {
		t.Fatalf("handle call evidence attributed to the wrong declaration:\n%s", out.String())
	}
}
