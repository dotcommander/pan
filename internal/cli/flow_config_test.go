package cli_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/pipeline/seed"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestFlowConfigSetupAndRefusal(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "pipelines")
	args := []string{"flow", "config", "--directory", dir}
	if err := cli.Run(context.Background(), args, newTestDeps(io.Discard)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read-only config created directory: %v", err)
	}
	args = append(args, "--setup")
	if err := cli.Run(context.Background(), args, newTestDeps(io.Discard)); err != nil {
		t.Fatal(err)
	}
	for _, name := range seed.Files() {
		if _, err := spec.Load(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cli.Run(context.Background(), args, newTestDeps(io.Discard)); err == nil {
		t.Fatal("expected refusal")
	}
	if err := cli.Run(context.Background(), append(args, "--force"), newTestDeps(io.Discard)); err != nil {
		t.Fatal(err)
	}
}
