package improve

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCensusReadsExistingCoverProfileWithoutRunningTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	profile := filepath.Join(dir, "coverage.out")
	writeFile(t, dir, "coverage.out", "mode: set\nsample/demo.go:1.1,1.10 1 1\n")
	report, err := RunCensus(context.Background(), CensusOptions{RepoPath: dir, CoverProfile: profile})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "set" || report.Files != 1 || report.TotalCoverage != 100 {
		t.Fatalf("coverprofile report = %+v", report)
	}
}

func TestCensusOwnedCopyLifecycle(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("isolated Go suite")
	}
	for _, mode := range []string{"success", "cancelled", "tampered"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, root, "go.mod", "module sample\n\ngo 1.26\n")
			writeFile(t, root, "demo.go", "package sample\n\nfunc Double(n int) int { return n * 2 }\n")
			writeFile(t, root, "demo_test.go", censusLifecycleFixture(mode == "tampered"))
			t.Cleanup(func() { cleanCensusSidecars(t, root) })
			ctx := t.Context()
			if mode == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			report, err := RunCensus(ctx, CensusOptions{RepoPath: root})
			if mode == "success" {
				if err != nil || report.TotalCoverage != 100 {
					t.Fatalf("census = %+v, %v", report, err)
				}
			} else if err == nil {
				t.Fatal("expected suite cancellation or ownership cleanup error")
			}
			owners := censusSidecars(t, root)
			if mode != "tampered" {
				if len(owners) != 0 {
					t.Fatalf("census left ownership records: %v", owners)
				}
				return
			}
			if len(owners) != 1 {
				t.Fatalf("retained ownership records = %v", owners)
			}
			dir := strings.TrimSuffix(owners[0], ".pan-owner.json")
			info, statErr := os.Stat(dir)
			if statErr != nil || !info.IsDir() || !strings.Contains(err.Error(), dir) {
				t.Fatalf("retained copy %q: stat=%v, error=%v", dir, statErr, err)
			}
		})
	}
}

func censusLifecycleFixture(tamper bool) string {
	if !tamper {
		return `package sample
import "testing"
func TestDouble(t *testing.T) {
 t.Parallel()
 if Double(2) != 4 { t.Fatal("unexpected result") }
}
`
	}
	return `package sample
import (
 "encoding/json"
 "os"
 "testing"
)
func TestDouble(t *testing.T) {
 t.Parallel()
 if Double(2) != 4 { t.Fatal("unexpected result") }
 dir, err := os.Getwd()
 if err != nil { t.Fatal(err) }
 owner := dir + ".pan-owner.json"
 data, err := os.ReadFile(owner)
 if err != nil { t.Fatal(err) }
 var record map[string]any
 if err := json.Unmarshal(data, &record); err != nil { t.Fatal(err) }
 record["directory"] = dir + "-mismatch"
 data, err = json.Marshal(record)
 if err != nil { t.Fatal(err) }
 if err := os.WriteFile(owner, data, 0600); err != nil { t.Fatal(err) }
}
`
}

func censusSidecars(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(os.TempDir(), "pan-improve-*.pan-owner.json"))
	if err != nil {
		t.Fatal(err)
	}
	var owned []string
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record worktreeOwnership
		if json.Unmarshal(data, &record) == nil && record.Repository == root {
			owned = append(owned, path)
		}
	}
	return owned
}

func cleanCensusSidecars(t *testing.T, root string) {
	t.Helper()
	for _, path := range censusSidecars(t, root) {
		dir := strings.TrimSuffix(path, ".pan-owner.json")
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
			continue
		}
		data, err := json.Marshal(worktreeOwnership{Directory: dir, Repository: root})
		if err != nil {
			t.Error(err)
			continue
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Error(err)
			continue
		}
		if err := removeOwnedCopy(dir); err != nil {
			t.Error(err)
		}
	}
}
