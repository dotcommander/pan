package app

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/config"
)

func TestRoadmapAcquisitionP95(t *testing.T) {
	repos := strings.Split(os.Getenv("PAN_BENCH_REPOS"), ":")
	if len(repos) != 2 || repos[0] == "" || repos[1] == "" {
		t.Skip("set PAN_BENCH_REPOS to two repositories")
	}
	for _, root := range repos {
		root := root
		t.Run(root, func(t *testing.T) {
			cfg := config.Default()
			cacheDir := t.TempDir()
			liveSvc := New(Deps{Config: cfg, SnapshotSource: "live", CacheDir: cacheDir})
			warmed, _, err := liveSvc.CacheWarm(context.Background(), root, cacheDir)
			if err != nil {
				t.Fatal(err)
			}
			autoSvc := New(Deps{Config: cfg, SnapshotSource: "auto", CacheDir: cacheDir})
			cached, err := autoSvc.Snapshot(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if warmed.Status.Snapshot.ID != cached.Status.Snapshot.ID {
				t.Fatal("cache changed generation identity")
			}
			warmed.Status.Snapshot, cached.Status.Snapshot = nil, nil
			warmed.Captured, cached.Captured = nil, nil
			warmed.CapturedStamps, cached.CapturedStamps = nil, nil
			wb, _ := json.Marshal(warmed)
			cb, _ := json.Marshal(cached)
			if string(wb) != string(cb) {
				t.Fatal("cache changed compiled snapshot output")
			}
			live := sampleP95(t, 20, func() error { _, err := liveSvc.Snapshot(context.Background(), root); return err })
			hit := sampleP95(t, 20, func() error { _, err := autoSvc.Snapshot(context.Background(), root); return err })
			staleCfg := cfg
			staleCfg.MaxNodes++
			staleSvc := New(Deps{Config: staleCfg, SnapshotSource: "auto", CacheDir: cacheDir})
			stale := sampleP95(t, 20, func() error { _, err := staleSvc.Snapshot(context.Background(), root); return err })
			t.Logf("live_p95=%s cache_p95=%s stale_fallback_p95=%s improvement=%.1f%%", live, hit, stale, 100*(1-float64(hit)/float64(live)))
		})
	}
}

func sampleP95(t *testing.T, n int, fn func() error) time.Duration {
	t.Helper()
	d := make([]time.Duration, n)
	for i := range d {
		start := time.Now()
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		d[i] = time.Since(start)
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[(95*n+99)/100-1]
}
