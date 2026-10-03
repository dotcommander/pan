package cache

import (
	"context"
	"encoding/json"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitEmptyCaptureRoundTripAndMissingCaptureRejection(t *testing.T) {
	t.Parallel()
	root, dir := t.TempDir(), t.TempDir()
	cfg := config.Default()
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Store(dir, root, cfg, snap, snap.Stamps()); err != nil {
		t.Fatal(err)
	}
	status := Inspect(context.Background(), root, dir, cfg)
	if !status.Usable || status.Stale {
		t.Fatalf("empty inspection %#v", status)
	}
	loaded, status, err := LoadValidated(context.Background(), root, dir, cfg)
	if err != nil || !status.Usable || loaded.Captured == nil || len(loaded.Files) != 0 {
		t.Fatalf("empty load %#v %#v %v", loaded, status, err)
	}
	snap.Captured = nil
	if _, err := Store(t.TempDir(), root, cfg, snap, snap.Stamps()); err == nil {
		t.Fatal("missing capture must be distinct from explicit empty capture")
	}
}

func TestInspectAndLoadShareIdentityValidation(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "mismatch", true: "missing"}[missing], func(t *testing.T) {
			t.Parallel()
			root, dir := t.TempDir(), t.TempDir()
			cfg := config.Default()
			snap, err := analyze.Build(context.Background(), root, cfg)
			if err != nil {
				t.Fatal(err)
			}
			path, err := Store(dir, root, cfg, snap, snap.Stamps())
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var entry Entry
			if err := json.Unmarshal(data, &entry); err != nil {
				t.Fatal(err)
			}
			want := "snapshot_identity_mismatch"
			if missing {
				entry.Snapshot.Status.Snapshot = nil
				want = "snapshot_identity_missing"
			} else {
				entry.Snapshot.Status.Snapshot.ID = "invalid"
			}
			data, err = json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			inspected := Inspect(context.Background(), root, dir, cfg)
			_, loaded, err := LoadValidated(context.Background(), root, dir, cfg)
			if err == nil || inspected.Usable || loaded.Usable || inspected.Reason != want || loaded.Reason != want {
				t.Fatalf("inspection %#v load %#v error %v", inspected, loaded, err)
			}
		})
	}
}

func TestFingerprintIncludesNormalizedInstructionLimit(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	baseline := Fingerprint(cfg)
	cfg.MaxInstructions++
	if Fingerprint(cfg) == baseline {
		t.Fatal("instruction bound omitted")
	}
	cfg = config.Default()
	cfg.MaxInstructions = 0
	if Fingerprint(cfg) != baseline {
		t.Fatal("zero instruction bound should normalize")
	}
}

func TestClearPropagatesNonAbsenceErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A nonempty directory occupying the owned file path makes Remove fail
	// without depending on elevated-user permission behavior.
	if err := os.Mkdir(filepath.Join(dir, EntryFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, EntryFile, "retained"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Clear(dir); err == nil {
		t.Fatal("remove failure discarded")
	}
	absent := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := Clear(absent); err != nil {
			t.Fatal(err)
		}
	}
}
