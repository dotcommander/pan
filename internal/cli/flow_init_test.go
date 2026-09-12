package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
	"gopkg.in/yaml.v3"
)

func TestFlowInitWritesStarterSpecAndGitignore(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "project: safe # title")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", repo, "flow", "init"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(repo, "pan.yaml")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Title  string `yaml:"title"`
		Phases []struct {
			Stages []map[string]any `yaml:"stages"`
		} `yaml:"phases"`
	}
	if parseErr := yaml.Unmarshal(data, &doc); parseErr != nil {
		t.Fatal(parseErr)
	}
	if doc.Title != filepath.Base(repo) || len(doc.Phases) != 3 {
		t.Fatalf("starter spec = %#v", doc)
	}
	for _, want := range []string{"fork:", "fanout:", "# stores:"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("starter spec missing %q", want)
		}
	}
	gitignore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gitignore) != "out/\n" {
		t.Fatalf(".gitignore = %q", gitignore)
	}
	if !strings.Contains(out.String(), `"output"`) {
		t.Fatalf("result missing output: %s", out.String())
	}
}

func TestFlowInitRefusesExistingUnlessForcedAndKeepsGitignoreIdempotent(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	specPath := filepath.Join(repo, "pan.yaml")
	if err := os.WriteFile(specPath, []byte("title: hand written\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("out/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cli.Run(context.Background(), []string{"--repo", repo, "flow", "init"}, newTestDeps(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want existing-file refusal", err)
	}
	if runErr := cli.Run(context.Background(), []string{"--repo", repo, "flow", "init", "--force"}, newTestDeps(&bytes.Buffer{})); runErr != nil {
		t.Fatal(runErr)
	}
	info, err := os.Stat(specPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
	}
	gitignore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gitignore) != "out/\n" {
		t.Fatalf(".gitignore duplicated entry: %q", gitignore)
	}
}

func TestFlowInitRefusesSymlinkTarget(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.yaml")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo, "pan.yaml")); err != nil {
		t.Fatal(err)
	}
	err := cli.Run(context.Background(), []string{"--repo", repo, "flow", "init", "--force"}, newTestDeps(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want symlink refusal", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("symlink target = %q", data)
	}
}
