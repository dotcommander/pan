package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestPipelineStoryboardRefreshFailureFallsBack(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/storyboardfallback\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPipelineStoryboardRefreshFailureHelper$")
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") && !strings.HasPrefix(entry, "XDG_CONFIG_HOME=") && !strings.HasPrefix(entry, "APPDATA=") {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env,
		"PAN_STORYBOARD_REFRESH_HELPER=1",
		"PAN_STORYBOARD_REFRESH_ROOT="+root,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"APPDATA="+filepath.Join(home, "appdata"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("refresh fallback helper: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fallback succeeded") {
		t.Fatalf("refresh fallback helper output = %q", out)
	}
}

func TestPipelineStoryboardRefreshFailureHelper(t *testing.T) {
	if os.Getenv("PAN_STORYBOARD_REFRESH_HELPER") != "1" {
		return
	}
	root := os.Getenv("PAN_STORYBOARD_REFRESH_ROOT")
	output, err := spec.ScanOutputPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "unreachable.yaml"), output); err != nil {
		t.Fatal(err)
	}
	result, err := New(Deps{Config: config.Default()}).PipelineStoryboard(context.Background(), root, StoryboardOptions{
		CommandHelp:  "off",
		RefreshSpec:  true,
		RefreshAfter: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RefreshPath != "" {
		t.Fatalf("refresh path = %q, want empty after failed refresh", result.RefreshPath)
	}
	if result.Storyboard.Coverage == nil {
		t.Fatal("fallback did not scan the repository")
	}
	fmt.Println("fallback succeeded")
}
