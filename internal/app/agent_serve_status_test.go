package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/config"
)

// TestAgentSnapshotStatusTracksRebuilds verifies snapshot/status accounting:
// the first call builds evidence, unchanged evidence is reused without a
// rebuild, and a content change forces exactly one rebuild.
func TestAgentSnapshotStatusTracksRebuilds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "fixture.go")
	if err := os.WriteFile(path, []byte("package fixture\nfunc Main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(Deps{Config: config.Config{MaxFiles: 20, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, MaxInstructions: 5, OutputBudget: 1024, CommandTimeout: time.Second}})
	state := service.AgentServe(root)
	ctx := context.Background()

	first, err := state.AgentSnapshotStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Ready || first.Rebuilds != 1 || !first.Complete {
		t.Fatalf("first status = %#v", first)
	}
	if first.BuiltAt == "" || first.VerifiedAt == "" || first.SnapshotID == "" || first.Source == "" {
		t.Fatalf("first status missing identity fields: %#v", first)
	}

	second, err := state.AgentSnapshotStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Rebuilds != 1 {
		t.Fatalf("unchanged evidence rebuilt: second = %#v", second)
	}

	if err := os.WriteFile(path, []byte("package fixture\nfunc Mane() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := state.AgentSnapshotStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if third.Rebuilds != 2 || !third.Ready {
		t.Fatalf("changed evidence did not rebuild exactly once: third = %#v", third)
	}
	if third.SnapshotID == first.SnapshotID {
		t.Fatalf("snapshot id did not change after rebuild: %#v", third)
	}
}
