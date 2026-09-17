package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/checks"
	"github.com/dotcommander/pan/internal/cli"
)

func TestScanChecksReportsFindingsWithoutCommandFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	content := "---\nname: InvalidSkill\ndescription: Valid.\n---\n"
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"--repo", root, "--format", "json", "scan", "checks", "--check", "skill-contract"}
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatalf("finding must not fail command: %v", err)
	}
	var envelope struct {
		Schema  string        `json:"schema_version"`
		Command []string      `json:"command"`
		Result  checks.Report `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if envelope.Schema != "pan/v1" || strings.Join(envelope.Command, " ") != "scan checks" {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope.Result.Schema != checks.Schema || envelope.Result.Checks[0].Status != checks.StatusFailed {
		t.Fatalf("result = %#v", envelope.Result)
	}
}

func TestScanChecksList(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--format", "json", "scan", "checks", "--list"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id": "skill-contract"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestScanChecksHelp(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"scan", "checks", "--help"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Usage: pan scan checks", "--check", "--list", "<target>"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, out.String())
		}
	}
}
