package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/repo"
)

func TestContextPreflightEmitsRepositoryContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePreflightFile(t, root, "AGENTS.md", "root guidance")
	writePreflightFile(t, root, "README.md", "# Pan fixture\nRepository purpose.")
	target := writePreflightFile(t, root, "internal/example.go", "package internal")

	var out bytes.Buffer
	args := []string{"--repo", root, "--format", "json", "context", "preflight", target}
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}

	var envelope struct {
		Schema  string               `json:"schema_version"`
		Command []string             `json:"command"`
		Result  repo.PreflightResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if envelope.Schema != "pan/v1" {
		t.Fatalf("schema_version = %q, want pan/v1", envelope.Schema)
	}
	if strings.Join(envelope.Command, " ") != "context preflight" {
		t.Fatalf("command = %v, want context preflight", envelope.Command)
	}
	if len(envelope.Result.Guidance) != 1 || envelope.Result.Guidance[0] != filepath.Join(root, "AGENTS.md") {
		t.Fatalf("guidance = %v", envelope.Result.Guidance)
	}
	if envelope.Result.Purpose == nil || envelope.Result.Purpose.Summary != "Repository purpose." {
		t.Fatalf("purpose = %#v", envelope.Result.Purpose)
	}
}

func TestContextPreflightStandaloneAllowsExternalTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := writePreflightFile(t, t.TempDir(), "single.txt", "standalone")
	var out bytes.Buffer
	args := []string{"--repo", root, "--standalone", "--format", "json", "context", "preflight", target}
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result repo.PreflightResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.Mode != "standalone" || !envelope.Result.ContextResolved {
		t.Fatalf("result = %#v", envelope.Result)
	}
}

func TestContextPreflightRejectsUnmappedExternalTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := writePreflightFile(t, t.TempDir(), "outside.txt", "outside")
	err := cli.Run(context.Background(), []string{"--repo", root, "context", "preflight", target}, newTestDeps(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "target must be within repository_root") {
		t.Fatalf("error = %v", err)
	}
}

func TestContextPreflightHelp(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"context", "preflight", "--help"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Usage: pan context preflight", "<target>", "--standalone"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, out.String())
		}
	}
}

func writePreflightFile(t *testing.T, root, name, contents string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
