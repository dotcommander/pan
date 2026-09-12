package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestAuditParityCommandsValidateSharedSelectors(t *testing.T) {
	t.Parallel()
	commands := []interface{ Validate() error }{
		cli.ReviewBriefCmd{Limit: 3, TopFiles: 2, Language: "go", Intent: "http", HistoryWindow: 7, RefactorSignatures: true},
		cli.ReviewRisksCmd{Limit: 3, TopFiles: 2, Language: "go", Intent: "http"},
		cli.ReviewEffectsCmd{Limit: 3, TopFiles: 2, Language: "go", Intent: "http", Kind: "http"},
		cli.RisksCmd{Limit: 3, TopFiles: 2, Language: "go", Intent: "http"},
		cli.SurfaceCmd{Limit: 3, TopFiles: 2, Language: "go", Intent: "http"},
		cli.EffectsCmd{Limit: 3, TopFiles: 2, Language: "go", Intent: "http", Kind: "http"},
	}
	for _, command := range commands {
		if err := command.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	}
	if err := (cli.ReviewRisksCmd{Language: "fortran"}).Validate(); err == nil {
		t.Fatal("unsupported language accepted")
	}
}

func TestReviewBriefIncludesRequestedHistoryAndSignatures(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeAuditFixture(t, repo, "main.go", "package main\nfunc Handle(v map[string][]string) { panic(\"x\") }\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "main.go"}, {"-c", "user.name=Pan Test", "-c", "user.email=pan@example.invalid", "commit", "-qm", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "review", "brief", "--limit", "1", "--language", "go", "--history-window", "7", "--refactor-signatures"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"\"history\"", "\"refactor_signatures\"", "\"read_queue\""} {
		if !strings.Contains(out.String(), field) {
			t.Fatalf("brief output missing %s: %s", field, out.String())
		}
	}
}

func TestScanRisksLanguageFiltersBeforeLimit(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeAuditFixture(t, repo, "main.go", "package main\nfunc main() { panic(\"x\") }\n")
	writeAuditFixture(t, repo, "job.py", "def job():\n    pass  # TODO: handle failure\n")
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "scan", "risks", "--top-files", "1", "--language", "python", "--intent", "job"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "main.go") || !strings.Contains(out.String(), "job.py") {
		t.Fatalf("language-scoped risks = %s", out.String())
	}
}

func TestRiskDetailProjectionParity(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeAuditFixture(t, repo, "main.go", "package main\nfunc main() { panic(\"x\") }\n")
	for _, detail := range []string{"compact", "evidence", "paths"} {
		results := make([]any, 0, 2)
		for _, command := range [][]string{{"scan", "risks"}, {"review", "risks"}} {
			var out bytes.Buffer
			args := append([]string{"--repo", repo, "--format", "json"}, command...)
			args = append(args, "--detail", detail)
			if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
				t.Fatalf("%s %v: %v", detail, command, err)
			}
			var envelope struct {
				Result any `json:"result"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("decode %s %v: %v", detail, command, err)
			}
			results = append(results, envelope.Result)
		}
		if !reflect.DeepEqual(results[0], results[1]) {
			t.Fatalf("%s scan/review results differ:\nscan=%#v\nreview=%#v", detail, results[0], results[1])
		}
		result := results[0].(map[string]any)
		omitted := result["omitted_fields"].([]any)
		switch detail {
		case "compact":
			want := []any{"files[].score_components", "lanes"}
			if !reflect.DeepEqual(omitted, want) {
				t.Fatalf("compact omitted_fields = %v, want %v", omitted, want)
			}
			if _, ok := result["lanes"]; ok {
				t.Fatal("compact risk result retained duplicated top-level lanes")
			}
		case "evidence":
			if len(omitted) != 0 {
				t.Fatalf("evidence omitted_fields = %v, want []", omitted)
			}
		}
		for i := 1; i < len(omitted); i++ {
			if omitted[i-1].(string) > omitted[i].(string) {
				t.Fatalf("%s omitted_fields not sorted: %v", detail, omitted)
			}
		}
	}
}

func writeAuditFixture(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
