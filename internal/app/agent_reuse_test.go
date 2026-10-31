package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/agent"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/review"
)

func TestAgentSessionUsesSnapshotCaptureForLiveAndCachedSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "fixture.go")
	original := []byte("package fixture\nfunc Main() {}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("API_KEY=synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MaxFiles: 20, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, MaxInstructions: 5, OutputBudget: 1024, CommandTimeout: time.Second}
	cacheDir := t.TempDir()
	live := New(Deps{Config: cfg, SnapshotSource: "live", CacheDir: cacheDir})
	if _, _, err := live.CacheWarm(context.Background(), root, cacheDir); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		service Service
		source  string
	}{
		{"live", live, "live"},
		{"cache", New(Deps{Config: cfg, SnapshotSource: "auto", CacheDir: cacheDir}), "cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := tc.service.AgentServe(root)
			status, err := state.AgentSnapshotStatus(context.Background())
			if err != nil || status.Source != tc.source {
				t.Fatalf("snapshot status=%+v err=%v", status, err)
			}
			row := review.ReadItem{Path: "fixture.go"}
			packet, err := state.AgentContext(context.Background(), review.Document{ReportID: "report"}, []review.ReadItem{row, {Path: ".env"}}, 4096)
			if err != nil || len(packet.Targets) != 1 || packet.Targets[0].Content != string(original) || len(packet.Omissions) != 1 {
				t.Fatalf("context=%+v err=%v", packet, err)
			}
			if _, retained := state.sources[".env"]; retained {
				t.Fatal("secret source retained")
			}
		})
	}
}

func TestAgentCaptureUsesBytesAndStampsFromSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MaxFiles: 10, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 10}
	state := New(Deps{Config: cfg, SnapshotSource: "live"}).AgentServe(root)
	stamps, err := analyze.Stamps(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The synthetic captured bytes distinguish snapshot reuse from a second disk read.
	snap := analyze.Snapshot{Files: []analyze.File{{Path: "fixture.go", Size: 6}}, Captured: map[string][]byte{"fixture.go": []byte("cached")}, CapturedStamps: stamps}
	if err := state.captureSources(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	if got := state.sources["fixture.go"].Content; got != "cached" {
		t.Fatalf("captured content=%q", got)
	}
	if state.sourcesFresh(context.Background()) {
		t.Fatal("content validation accepted differing on-disk bytes with equal stamps")
	}
	snap.Captured = nil
	if err := state.captureSources(context.Background(), snap); err == nil {
		t.Fatal("missing captured bytes accepted")
	}
}

func TestAgentRankingReuseIsolatedAndInvalidated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "fixture.go")
	original := []byte("package fixture\nfunc Main() {}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MaxFiles: 20, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, MaxInstructions: 5, OutputBudget: 1024, CommandTimeout: time.Second}
	state := New(Deps{Config: cfg, SnapshotSource: "live"}).AgentServe(root)
	ctx := context.Background()
	_, ranked, err := state.rankedSnapshot(ctx)
	if err != nil || len(ranked) == 0 || len(ranked[0].Symbols) == 0 {
		t.Fatalf("ranked=%+v err=%v", ranked, err)
	}
	first := &state.ranked[0]
	originalName, originalScore := ranked[0].Symbols[0].Name, ranked[0].Score
	ranked[0].Symbols[0].Name = "poisoned"
	ranked[0].Components["poisoned"] = 1
	ranked[0].Path = "poisoned"
	if _, err := state.AgentMapRender(ctx, "verbose"); err != nil {
		t.Fatal(err)
	}
	if state.ranked[0].DetailLevel != 0 {
		t.Fatal("map rendering modified cached detail level")
	}
	_, again, err := state.rankedSnapshot(ctx)
	if err != nil || &state.ranked[0] != first || again[0].Path == "poisoned" || again[0].Symbols[0].Name != originalName || again[0].Score != originalScore {
		t.Fatalf("cached ranking changed or was recomputed: %+v err=%v", again, err)
	}
	if _, poisoned := again[0].Components["poisoned"]; poisoned {
		t.Fatal("cached component map was modified")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(strings.Replace(string(original), "Main", "Mane", 1))
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := state.AgentVerify(ctx, review.Document{ReportID: "report"}); !errors.Is(err, agent.ErrStaleEvidence) {
		t.Fatalf("same-size/timestamp mutation verify=%v", err)
	}
	_, refreshed, err := state.rankedSnapshot(ctx)
	if err != nil || state.rebuilds != 2 || len(refreshed) == 0 || refreshed[0].Symbols[0].Name != "Mane" {
		t.Fatalf("ranking did not refresh: %+v rebuilds=%d err=%v", refreshed, state.rebuilds, err)
	}
}
