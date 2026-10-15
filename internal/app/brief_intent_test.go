package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/config"
)

func TestBriefPrioritizesExplicitIntentOverStructuralHubs(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("source-analysis integration fixture")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                  "module example.com/brief\n\ngo 1.25\n",
		"internal/agent/serve.go": "package agent\nfunc decodeServeObject() {}\n",
	}
	for i := range 24 {
		files[fmt.Sprintf("internal/hub/hub%d.go", i)] = fmt.Sprintf("package hub\nfunc Hub%d() {}\n", i)
	}
	for i := range 6 {
		files[fmt.Sprintf("cmd/tool%d/main.go", i)] = "package main\nimport \"example.com/brief/internal/hub\"\nfunc main() { hub.Hub0() }\n"
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	goal := "Locate decodeServeObject: rejecting multiple JSON objects in one NDJSON JSON-RPC request line."
	result, err := New(Deps{Config: config.Default()}).Brief(context.Background(), root, goal, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) == 0 || result.Entries[0].Path != "internal/agent/serve.go" {
		t.Fatalf("explicit decoder displaced by structural hubs: %#v", result.Entries)
	}
	if !strings.Contains(result.Map.Text, "internal/agent/serve.go") {
		t.Fatalf("selected decoder absent from map: %s", result.Map.Text)
	}
	if result.Map.TotalFiles != len(result.Entries) {
		t.Fatalf("entries/map differ: %d/%d", len(result.Entries), result.Map.TotalFiles)
	}
}
