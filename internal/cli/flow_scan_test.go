package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"gopkg.in/yaml.v3"
)

func TestFlowScanWritesSpecAndEnvelope(t *testing.T) {
	t.Parallel()
	output := filepath.Join(t.TempDir(), "scan.yaml")
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "flow", "scan", "-o", output})
	for _, want := range []string{`"output":`, `"root":`, `"updated": false`, `"outside_repository": true`} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Root string `yaml:"root"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(doc.Root) {
		t.Fatalf("embedded root = %q, want absolute", doc.Root)
	}
}

func TestFlowScanStdoutIsRawYAML(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "flow", "scan", "-o", "-"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "schema_version") || !strings.Contains(out.String(), "root:") {
		t.Fatalf("stdout = %q, want raw YAML", out.String())
	}
}

func TestFlowScanPositionalPathOverridesRepo(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	path := httpGoRepo()
	err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "flow", "scan", path, "-o", "-"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Root string `yaml:"root"`
	}
	if decodeErr := yaml.Unmarshal(out.Bytes(), &doc); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	want, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Root != want {
		t.Fatalf("root = %q, want positional path %q", doc.Root, want)
	}
}

func TestFlowScanDefaultOutputUsesIsolatedUserConfig(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	repo, err := filepath.Abs(basicGoRepo())
	if err != nil {
		t.Fatal(err)
	}
	output := runFlowScanHelper(t, homeDir, repo, false)
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("default output %s: %v", output, err)
	}
	runFlowScanHelper(t, homeDir, repo, true)
}

func TestFlowScanDefaultOutputHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("PAN_FLOW_SCAN_HELPER") != "1" {
		return
	}
	err := cli.Run(context.Background(), []string{"--repo", os.Getenv("PAN_FLOW_SCAN_REPO"), "flow", "scan"}, newTestDeps(os.Stdout))
	if os.Getenv("PAN_FLOW_SCAN_REFUSE") == "1" {
		if err == nil {
			fmt.Fprintln(os.Stderr, "expected existing default output refusal")
			os.Exit(2)
		}
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	output, pathErr := spec.ScanOutputPath(os.Getenv("PAN_FLOW_SCAN_REPO"))
	if pathErr != nil {
		fmt.Fprintln(os.Stderr, pathErr)
		os.Exit(3)
	}
	if _, printErr := fmt.Fprintln(os.Stdout, "DEFAULT_OUTPUT="+output); printErr != nil {
		t.Fatal(printErr)
	}
}

func runFlowScanHelper(t *testing.T, homeDir, repo string, refuse bool) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFlowScanDefaultOutputHelper$")
	env := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") && !strings.HasPrefix(entry, "XDG_CONFIG_HOME=") && !strings.HasPrefix(entry, "APPDATA=") {
			env = append(env, entry)
		}
	}
	env = append(env, "PAN_FLOW_SCAN_HELPER=1", "PAN_FLOW_SCAN_REPO="+repo, "HOME="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, "config"), "APPDATA="+filepath.Join(homeDir, "appdata"))
	if refuse {
		env = append(env, "PAN_FLOW_SCAN_REFUSE=1")
	}
	cmd.Env = env
	out, commandErr := cmd.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("scan helper (refuse=%t): %v\n%s", refuse, commandErr, out)
	}
	if refuse {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if output, found := strings.CutPrefix(line, "DEFAULT_OUTPUT="); found {
			return output
		}
	}
	t.Fatalf("scan helper did not report default output:\n%s", out)
	return ""
}

func TestFlowScanStdoutRejectsJSON(t *testing.T) {
	t.Parallel()
	err := cli.Run(context.Background(), []string{"--format", "json", "flow", "scan", "-o", "-"}, newTestDeps(io.Discard))
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("error = %v, want JSON incompatibility", err)
	}
}

func TestFlowScanRefusesUnrelatedOutputDuringUpdate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	update := filepath.Join(dir, "update.yaml")
	output := filepath.Join(dir, "unrelated.yaml")
	updateBody := []byte("title: Hand Written\nbreadcrumb: Keep me\nstores: []\n")
	if err := os.WriteFile(update, updateBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "flow", "scan", "--update", update, "-o", output}, newTestDeps(io.Discard))
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("error = %v, want refusal", err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do not replace" {
		t.Fatalf("unrelated output = %q", got)
	}
}

func TestFlowScanUpdatePreservesHumanFieldsAndRoot(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "update.yaml")
	body := []byte("title: Hand Written\nbreadcrumb: Keep me\nstores: []\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "flow", "scan", "--update", path, "-o", path}, newTestDeps(io.Discard)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Title      string `yaml:"title"`
		Breadcrumb string `yaml:"breadcrumb"`
		Root       string `yaml:"root"`
		Phases     []any  `yaml:"phases"`
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "Hand Written" || got.Breadcrumb != "Keep me" || !filepath.IsAbs(got.Root) || len(got.Phases) == 0 {
		t.Fatalf("merged spec = %#v", got)
	}
}

func TestFlowScanForcePreservesExistingMode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "scan.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "flow", "scan", "--force", "-o", path}, newTestDeps(io.Discard)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestFlowScanRejectsNegativeSizing(t *testing.T) {
	t.Parallel()
	err := cli.Run(context.Background(), []string{"flow", "scan", "--max-phases=-1"}, newTestDeps(io.Discard))
	if err == nil || !strings.Contains(err.Error(), "max phases must be non-negative") {
		t.Fatalf("error = %v", err)
	}
}

func TestFlowScanStdoutShortWriter(t *testing.T) {
	t.Parallel()
	err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "flow", "scan", "-o", "-"}, newTestDeps(shortWriter{n: 3}))
	if err == nil || !strings.Contains(err.Error(), "write scan to stdout") || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want short write", err)
	}
}

type shortWriter struct{ n int }

func (w shortWriter) Write([]byte) (int, error) { return w.n, nil }
