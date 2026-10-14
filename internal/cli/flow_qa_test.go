package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/cli"
)

func flowQARepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for file, source := range map[string]string{
		"go.mod": "module example.test/flowqa\n\ngo 1.26\n",
		"a/a.go": "package a\nfunc Leaf() {}\nfunc Run() { Leaf() }\n",
		"b/b.go": "package b\nfunc Run() {}\n",
		"use.go": "package flowqa\nimport (\"example.test/flowqa/a\"; \"example.test/flowqa/b\")\nfunc Use() { a.Run(); b.Run() }\n",
	} {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFlowAmbiguityEmitsJSONFailure(t *testing.T) {
	t.Parallel()
	root := flowQARepo(t)
	for _, command := range [][]string{{"flow", "calls"}, {"flow", "impact"}, {"impact"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			args := append([]string{"--repo", root, "--format", "json"}, command...)
			args = append(args, "Run")
			err := cli.Run(context.Background(), args, newTestDeps(&out))
			if cli.ExitCode(err) != 1 || err == nil {
				t.Fatalf("error=%v", err)
			}
			var envelope struct {
				Schema      string               `json:"schema_version"`
				Diagnostics []analyze.Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Schema != "pan/v1" || len(envelope.Diagnostics) == 0 || !strings.Contains(envelope.Diagnostics[0].Message, "symbol:a/a.go::Run") {
				t.Fatalf("missing structured alternatives: %s", out.String())
			}
		})
	}
}

func TestFlowImpactOnlyReturnsSelectedIncomingCallers(t *testing.T) {
	t.Parallel()
	root := flowQARepo(t)
	handle := "symbol:a/a.go::Run#function@3"
	var calls bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", root, "--format", "json", "flow", "calls", handle}, newTestDeps(&calls)); err != nil {
		t.Fatal(err)
	}
	var callEnvelope struct {
		Result []analyze.Edge `json:"result"`
	}
	if err := json.Unmarshal(calls.Bytes(), &callEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(callEnvelope.Result) != 2 {
		t.Fatalf("want incoming + outgoing, got %s", calls.String())
	}
	for _, command := range [][]string{{"flow", "impact"}, {"impact"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			args := append([]string{"--repo", root, "--format", "json"}, command...)
			args = append(args, handle)
			if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Result struct {
					Edges []analyze.Edge `json:"edges"`
				} `json:"result"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			edges := envelope.Result.Edges
			if len(edges) != 1 || edges[0].From != "Use" || edges[0].To != "Run" || edges[0].Target == nil || edges[0].Target.Path != "a/a.go" {
				t.Fatalf("wrong caller identity: %s", out.String())
			}
		})
	}
}
