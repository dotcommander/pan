package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/config"
)

func TestCleanApplyBlocksWhenGitStateIsUnknown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scratch.tmp"), []byte("discard me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(Deps{Config: config.Default()}).CleanApply(ctx, dir, nil, nil, true)
	if err == nil || !strings.Contains(err.Error(), "repository state is unknown") {
		t.Fatalf("CleanApply error = %v, want unknown repository state block", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch.tmp")); err != nil {
		t.Fatalf("blocked apply changed fixture: %v", err)
	}
}
