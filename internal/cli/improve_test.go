package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/config"
)

// newImproveDeps builds CLI deps whose improve policy points the run
// ledger at an isolated temporary state directory, so tests never touch
// or read the user's real history.
func newImproveDeps(t *testing.T, out *bytes.Buffer) cli.Deps {
	t.Helper()
	cfg := config.Config{
		MaxFiles: 100, MaxFileBytes: 1024, MaxTotalBytes: 4096,
		MaxNodes: 20, OutputBudget: 1024, CommandTimeout: 10 * time.Second,
	}
	cfg.Improve.StateDir = t.TempDir()
	return cli.Deps{App: app.New(app.Deps{Config: cfg}), Out: out}
}

// writeImproveFixture files a module fixture; names are slash-relative.
func writeImproveFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const fixtureGoMod = "module probe.example/demo\n\ngo 1.25\n"

const fixtureDeadCodeSrc = `package demo

import "fmt"

// Used is the exported surface.
func Used() string { return fmt.Sprintf("%d", helper()) }

func helper() int { return 1 }

// deadHelper has zero references anywhere in the module.
func deadHelper() int { return 2 }
`

func TestImproveProbeCommandValidatesWithoutApplying(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	repo := writeImproveFixture(t, map[string]string{
		"go.mod":  fixtureGoMod,
		"demo.go": fixtureDeadCodeSrc,
	})
	deps := newImproveDeps(t, &out)
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "probe"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"command": [`,
		`"improve",`,
		`"probe"`,
		`"schema": "pan.improve-probe/v1"`,
		`"applied": false`,
		`"outcome": "success"`,
		`"live_apply": "proposal-only`,
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("probe output missing %q:\n%s", want, out.String())
		}
	}
	if src, err := os.ReadFile(filepath.Join(repo, "demo.go")); err != nil || string(src) != fixtureDeadCodeSrc {
		t.Fatal("improve probe must not modify the target repository")
	}
}

func TestImproveRecommendCommandTasks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     map[string]string
		wantTask  string
		wantCmd   string
		wantField string
	}{
		{
			name: "dead code without history starts with prep",
			files: map[string]string{
				"go.mod":  fixtureGoMod,
				"demo.go": fixtureDeadCodeSrc,
			},
			wantTask:  "prep",
			wantCmd:   "improve prep --dry-run",
			wantField: `"dead_symbols": 0`,
		},
		{
			name: "clean module recommends prep",
			files: map[string]string{
				"go.mod":  fixtureGoMod,
				"demo.go": "package demo\n\nfunc Used() string { return \"clean\" }\n",
			},
			wantTask:  "prep",
			wantCmd:   "improve prep --dry-run",
			wantField: `"dead_symbols": 0`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			repo := writeImproveFixture(t, tt.files)
			deps := newImproveDeps(t, &out)
			err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "recommend"}, deps)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				`"schema": "pan.improve-recommend/v1"`,
				`"task": "` + tt.wantTask + `"`,
				`"command": "pan --repo ` + repo + ` ` + tt.wantCmd + `"`,
				tt.wantField,
			} {
				if !bytes.Contains(out.Bytes(), []byte(want)) {
					t.Fatalf("recommend output missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestImproveRefactorCommandRefusesNonGitTree(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	repo := writeImproveFixture(t, map[string]string{
		"go.mod":  fixtureGoMod,
		"demo.go": fixtureDeadCodeSrc,
	})
	deps := newImproveDeps(t, &out)
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "refactor"}, deps)
	if err != nil {
		t.Fatalf("a gate refusal is a completed run, not a command error: %v", err)
	}
	for _, want := range []string{
		`"schema": "pan.improve-refactor/v1"`,
		`"outcome": "preflight"`,
		`"success": false`,
		`"dry_run": true`,
		`"live_apply": "dry-run`,
		`is not a git work tree`,
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("refactor refusal missing %q:\n%s", want, out.String())
		}
	}
	if src, err := os.ReadFile(filepath.Join(repo, "demo.go")); err != nil || string(src) != fixtureDeadCodeSrc {
		t.Fatal("improve refactor must not modify the target repository")
	}
}

func TestImprovePrepCommandRefusesNonGitTree(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	repo := writeImproveFixture(t, map[string]string{
		"go.mod":  fixtureGoMod,
		"demo.go": fixtureDeadCodeSrc,
	})
	deps := newImproveDeps(t, &out)
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "prep"}, deps)
	if err != nil {
		t.Fatalf("a gate refusal is a completed run, not a command error: %v", err)
	}
	for _, want := range []string{
		`"schema": "pan.improve-prep/v1"`,
		`"outcome": "preflight"`,
		`"success": false`,
		`"dry_run": true`,
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("prep refusal missing %q:\n%s", want, out.String())
		}
	}
}

func TestImproveStatsCommandEmptyLedger(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	repo := writeImproveFixture(t, map[string]string{"go.mod": fixtureGoMod})
	deps := newImproveDeps(t, &out)
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "stats"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"schema": "pan.improve-stats/v1"`,
		`"records": 0`,
		`"total_runs": 0`,
		`"dry_run_only": true`,
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("stats output missing %q:\n%s", want, out.String())
		}
	}
}

func TestImproveCensusCommandMeasuresIsolatedCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: runs the go toolchain against an isolated copy")
	}
	t.Parallel()
	var out bytes.Buffer
	repo := writeImproveFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"demo.go": `package demo

import "fmt"

// Used is exercised by the suite.
func Used() string { return fmt.Sprintf("%d", helper()) }

func helper() int { return 1 }

// deadHelper is never exercised.
func deadHelper() int { return 2 }
`,
		"demo_test.go": `package demo

import "testing"

func TestUsed(t *testing.T) {
	t.Parallel()
	if Used() != "1" {
		t.Fatal("unexpected")
	}
}
`,
	})
	deps := newImproveDeps(t, &out)
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "census"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"schema": "pan.improve-census/v1"`,
		`"coverage_measured": true`,
		`"package": "."`,
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("census output missing %q:\n%s", want, out.String())
		}
	}
	if src, err := os.ReadFile(filepath.Join(repo, "demo.go")); err != nil || !bytes.Contains(src, []byte("deadHelper")) {
		t.Fatal("improve census must not modify the target repository")
	}
}

func TestImproveCommandsAreWiredNotStubs(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"recommend", "census", "probe", "prep", "refactor", "stats"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			repo := writeImproveFixture(t, map[string]string{"go.mod": fixtureGoMod})
			deps := newImproveDeps(t, &out)
			args := []string{"--repo", repo, "improve", name}
			// recommend and stats complete without a git tree; census,
			// prep, and refactor are toolchain or gate-refusing runs that
			// still complete as commands. The wiring assertion is that no
			// improve leaf reports the stub envelope.
			_ = cli.Run(context.Background(), args, deps)
			if bytes.Contains(out.Bytes(), []byte(`"status": "unavailable"`)) {
				t.Fatalf("improve %s still reports as an unavailable stub", name)
			}
		})
	}
}
