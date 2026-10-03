package storyboard

import (
	"context"
	"github.com/dotcommander/pan/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandHelpOffStaticNeverExecute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, mode := range []string{"off", "static"} {
		commands, _, err := ResolveCommandHelpWithOptions(context.Background(), mode, root, config.CommandHelpRules{})
		if err != nil || len(commands) != 0 {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}
func TestTrustedCommandHelpFixture(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("subprocess fixture")
	}
	root := t.TempDir()
	for name, data := range map[string]string{"go.mod": "module fixture\n\ngo 1.25\n", "main.go": "package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"Usage: fixture [flags]\\nTrusted fixture help\")}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commands, provenance, err := ResolveCommandHelpWithOptions(context.Background(), "execute", root, config.CommandHelpRules{InvocationTimeout: 4 * time.Second, Timeout: 5 * time.Second})
	if err != nil || provenance != "executed" || len(commands) != 1 {
		t.Fatalf("commands=%v provenance=%s err=%v", commands, provenance, err)
	}
}
func TestCommandHelpTraversalBounds(t *testing.T) {
	t.Parallel()
	c := commandHelpCollector{ctx: context.Background(), rules: config.Default().CommandHelp, seen: map[string]bool{}}
	c.rules.MaxDepth = 1
	if err := c.collect([]string{"a", "b"}); err == nil {
		t.Fatal("depth bound ignored")
	}
	c.invocations = c.rules.MaxInvocations
	if err := c.collect(nil); err == nil {
		t.Fatal("invocation bound ignored")
	}
}
