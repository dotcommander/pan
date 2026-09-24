package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestScanGraphExposesUnresolvedReferenceWithoutFalseEdges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for file, source := range map[string]string{
		"a.ts":   "export class Page {}\n",
		"b.ts":   "export class Page {}\n",
		"use.ts": "export function run() { return new Page() }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", root, "--format", "json", "scan", "graph"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result struct {
			Kinds      map[string]int `json:"kinds"`
			Unresolved []struct {
				Name   string `json:"name"`
				Reason string `json:"reason"`
			} `json:"unresolved"`
			UnresolvedCount int `json:"unresolved_count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.Kinds["references"] != 0 || envelope.Result.UnresolvedCount == 0 {
		t.Fatalf("graph did not distinguish resolved from unbound: %s", out.String())
	}
	found := false
	for _, item := range envelope.Result.Unresolved {
		if item.Name == "Page" && item.Reason == "no_import_binding" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing unbound Page explanation: %s", out.String())
	}
}
