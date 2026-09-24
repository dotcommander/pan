package cache

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

func TestCachedSnapshotRetainsResolvedAndUnresolvedEvidence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for file, contents := range map[string]string{
		"go.mod":     "module example.test/cached\n\ngo 1.25\n",
		"calls.go":   "package cached\nfunc Run() {}\nfunc Use() { Run() }\n",
		"model.ts":   "export class Widget {}\n",
		"caller.ts":  "import { Widget } from './model'\nnew Widget()\n",
		"unknown.ts": "new Widget()\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	live, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	live.Status.Snapshot = &analyze.SnapshotInfo{ID: SnapshotID(cfg, live, analyze.ManifestFromSnapshot(live))}
	cacheDir := t.TempDir()
	if _, err := Store(cacheDir, root, cfg, live, live.Stamps()); err != nil {
		t.Fatal(err)
	}
	cached, status, err := LoadValidated(context.Background(), root, cacheDir, cfg)
	if err != nil || !status.Usable || status.Stale {
		t.Fatalf("cached snapshot: status=%+v err=%v", status, err)
	}
	if !reflect.DeepEqual(live.Edges, cached.Edges) || !reflect.DeepEqual(live.UnresolvedReferences, cached.UnresolvedReferences) || live.UnresolvedCount != cached.UnresolvedCount {
		t.Fatalf("cache changed evidence: live=%+v cached=%+v", live, cached)
	}
}
