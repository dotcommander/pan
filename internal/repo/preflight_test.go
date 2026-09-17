package repo_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/repo"
)

type preflightFixture struct {
	Name       string            `json:"name"`
	Standalone bool              `json:"standalone"`
	Target     string            `json:"target"`
	Files      map[string]string `json:"files"`
	Symlinks   map[string]string `json:"symlinks"`
	Want       preflightWant     `json:"want"`
}

type preflightWant struct {
	Mode           string           `json:"mode"`
	Target         string           `json:"target"`
	Guidance       []string         `json:"guidance"`
	Purpose        *preflightValue  `json:"purpose"`
	RequiredChecks []preflightCheck `json:"required_checks"`
	ErrorContains  string           `json:"error_contains"`
}

type preflightValue struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

type preflightCheck struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

func TestPreflightCompatibilityCorpus(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "preflight_compatibility.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []preflightFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			runPreflightFixture(t, fixture)
		})
	}
}

func runPreflightFixture(t *testing.T, fixture preflightFixture) {
	t.Helper()
	root := t.TempDir()
	external := t.TempDir()
	resolve := fixturePathResolver(t, root, external)
	writePreflightTree(t, fixture, resolve)

	result, runErr := repo.Preflight(resolve(fixture.Target), root, fixture.Standalone)
	if fixture.Want.ErrorContains != "" {
		if runErr == nil || !strings.Contains(runErr.Error(), fixture.Want.ErrorContains) {
			t.Fatalf("error = %v, want substring %q", runErr, fixture.Want.ErrorContains)
		}
		return
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	assertPreflightResult(t, result, fixture.Want, resolve)
}

func writePreflightTree(t *testing.T, fixture preflightFixture, resolve func(string) string) {
	t.Helper()
	for name, value := range fixture.Files {
		path := resolve(name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range fixture.Symlinks {
		linkPath := resolve(link)
		if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(resolve(target), linkPath); err != nil {
			t.Fatal(err)
		}
	}
}

func fixturePathResolver(t *testing.T, root, external string) func(string) string {
	t.Helper()
	return func(value string) string {
		switch {
		case value == "root":
			return root
		case strings.HasPrefix(value, "root/"):
			return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(value, "root/")))
		case value == "external":
			return external
		case strings.HasPrefix(value, "external/"):
			return filepath.Join(external, filepath.FromSlash(strings.TrimPrefix(value, "external/")))
		default:
			t.Fatalf("unsupported fixture path %q", value)
			return ""
		}
	}
}

func assertPreflightResult(t *testing.T, result repo.PreflightResult, want preflightWant, resolve func(string) string) {
	t.Helper()
	if result.Mode != want.Mode {
		t.Errorf("mode = %q, want %q", result.Mode, want.Mode)
	}
	if want.Target != "" {
		wantTarget, err := filepath.EvalSymlinks(resolve(want.Target))
		if err != nil {
			t.Fatal(err)
		}
		if result.Target != wantTarget {
			t.Errorf("target = %q, want %q", result.Target, wantTarget)
		}
	}
	wantGuidance := make([]string, len(want.Guidance))
	for i, path := range want.Guidance {
		wantGuidance[i] = resolve(path)
	}
	if !reflect.DeepEqual(result.Guidance, wantGuidance) {
		t.Errorf("guidance = %#v, want %#v", result.Guidance, wantGuidance)
	}
	if want.Purpose == nil {
		if result.Purpose != nil {
			t.Errorf("purpose = %#v, want nil", result.Purpose)
		}
	} else {
		wantPurpose := &repo.PurposeSignal{Kind: want.Purpose.Kind, Path: resolve(want.Purpose.Path), Summary: want.Purpose.Summary}
		if !reflect.DeepEqual(result.Purpose, wantPurpose) {
			t.Errorf("purpose = %#v, want %#v", result.Purpose, wantPurpose)
		}
	}
	for _, required := range want.RequiredChecks {
		check := repo.PreflightCheck{Kind: required.Kind, Path: resolve(required.Path), Status: required.Status}
		if !containsPreflightCheck(result.Checks, check) {
			t.Errorf("required check %#v missing from %#v", check, result.Checks)
		}
	}
	if !result.ContextResolved {
		t.Error("context_resolved = false")
	}
}

func containsPreflightCheck(checks []repo.PreflightCheck, want repo.PreflightCheck) bool {
	for _, check := range checks {
		if check == want {
			return true
		}
	}
	return false
}
