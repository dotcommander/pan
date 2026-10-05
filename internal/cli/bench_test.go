package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

// benchFixture builds one git mirror and dataset under a temp root and
// returns the CLI arguments for a full bench retrieval run against it.
func benchFixture(t *testing.T) (args []string, mirrors string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	mirrors = filepath.Join(dir, "mirrors")
	mirror := filepath.Join(mirrors, "acme__widget")
	if err := os.MkdirAll(filepath.Join(mirror, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := "package widget\n\nimport \"fmt\"\n\n// ParseWidget parses the widget input text.\nfunc ParseWidget(input string) (string, error) {\n\treturn fmt.Sprintf(\"%s!\", input), nil\n}\n"
	if err := os.WriteFile(filepath.Join(mirror, "pkg", "widget.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "README.md"), []byte("# widget\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=pan", "GIT_AUTHOR_EMAIL=pan@example.com",
		"GIT_COMMITTER_NAME=pan", "GIT_COMMITTER_EMAIL=pan@example.com",
	)
	for _, args := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "add", "."},
		{"git", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = mirror
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", mirror, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(out))
	dataset := filepath.Join(dir, "dataset.jsonl")
	line, err := json.Marshal(map[string]any{
		"instance_id":       "acme__widget-1",
		"repo":              "acme/widget",
		"base_commit":       commit,
		"problem_statement": "ParseWidget should parse the widget input; fix the parser.",
		"patch":             "--- a/pkg/widget.go\n+++ b/pkg/widget.go\n@@ -1 +1 @@\n-x\n+y\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataset, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"--dataset", dataset, "--mirrors", mirrors, "--work", filepath.Join(dir, "work")}, mirrors
}

func TestBenchRetrievalJSON(t *testing.T) {
	t.Parallel()
	flags, _ := benchFixture(t)
	var out bytes.Buffer
	if err := cli.Run(t.Context(), append([]string{"--format", "json", "bench", "retrieval"}, flags...), newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result struct {
			Schema    string `json:"schema"`
			Instances int    `json:"instances"`
			Scored    int    `json:"scored"`
			Systems   []struct {
				System string `json:"system"`
				Cases  int    `json:"cases"`
			} `json:"systems"`
			Rows []struct {
				GoldPaths []string `json:"gold_paths"`
				PanPaths  []string `json:"pan_paths"`
			} `json:"rows"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	result := envelope.Result
	if result.Schema != "pan.retrieval-bench/v1" {
		t.Fatalf("schema = %q", result.Schema)
	}
	if result.Instances != 1 || result.Scored != 1 {
		t.Fatalf("instances=%d scored=%d\n%s", result.Instances, result.Scored, out.String())
	}
	if len(result.Systems) != 2 || result.Systems[0].System != "pan" || result.Systems[1].System != "bm25" {
		t.Fatalf("systems = %+v", result.Systems)
	}
	if len(result.Rows) != 1 || len(result.Rows[0].GoldPaths) != 1 || result.Rows[0].GoldPaths[0] != "pkg/widget.go" {
		t.Fatalf("rows = %+v", result.Rows)
	}
	if len(result.Rows[0].PanPaths) == 0 {
		t.Fatal("pan ranking is empty")
	}
}

func TestBenchRetrievalSkipsUnknownMirror(t *testing.T) {
	t.Parallel()
	flags, _ := benchFixture(t)
	// Point at an empty mirrors directory so the only instance skips.
	empty := t.TempDir()
	args := []string{"--format", "json", "bench", "retrieval", flags[0], flags[1], "--mirrors", empty, "--work", filepath.Join(t.TempDir(), "work")}
	var out bytes.Buffer
	if err := cli.Run(t.Context(), args, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"scored": 0`)) {
		t.Fatalf("expected zero scored instances:\n%s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mirror not found")) {
		t.Fatalf("expected explicit skip reason:\n%s", out.String())
	}
}

func TestBenchRetrievalValidatesFlags(t *testing.T) {
	t.Parallel()
	flags, _ := benchFixture(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"negative limit", append([]string{"bench", "retrieval"}, append(flags, "--limit=-1")...), "--limit must not be negative"},
		{"negative rows", append([]string{"bench", "retrieval"}, append(flags, "--top-rows=-1")...), "--top-rows must not be negative"},
		{"zero budget", append([]string{"bench", "retrieval"}, append(flags, "--token-budget", "0")...), "--token-budget must be positive"},
		{"missing dataset", []string{"bench", "retrieval", "--mirrors", t.TempDir()}, "missing flags: --dataset"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := cli.Run(t.Context(), test.args, newTestDeps(&out))
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(test.want)) {
				t.Fatalf("Run(%v) err = %v, want %q", test.args, err, test.want)
			}
		})
	}
}
