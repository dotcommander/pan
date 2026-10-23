package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/config"
)

func TestExcludePathScopesCapture(t *testing.T) {
	t.Parallel()
	for _, loaded := range []bool{false, true} {
		name := "injected"
		if loaded {
			name = "loaded"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, name := range []string{"main.go", "keep/one.go", "omit/gone.go", "native/vendor/gone.go", "vendor/gone.go", "configured/gone.go", "stray.go", "comma,name/gone.go"} {
				full := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("package fixture\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Default()
			cfg.Exclude = append(cfg.Exclude, "configured")
			original := slices.Clone(cfg.Exclude)
			var out bytes.Buffer
			deps := cli.Deps{App: app.New(app.Deps{Config: cfg, SnapshotSource: "live", CacheDir: t.TempDir()}), Out: &out}
			if loaded {
				deps.LoadConfig = func() (config.Config, error) { return cfg, nil }
			}
			args := []string{"--standalone", "--repo", root, "--format", "json", "--exclude-path", "./omit/", "--exclude-path", "native/vendor", "--exclude-path", "stray.go", "--exclude-path", "comma,name", "scan", "files", "--top", "0"}
			if err := cli.Run(t.Context(), args, deps); err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Result   []analyze.File `json:"result"`
				Analysis analyze.Status `json:"analysis"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, file := range envelope.Result {
				got = append(got, file.Path)
			}
			if !slices.Equal(got, []string{"keep/one.go", "main.go"}) {
				t.Fatalf("files=%v; output=%s", got, out.String())
			}
			for _, name := range []string{"omit", "native/vendor", "stray.go", "comma,name", "vendor", "configured"} {
				if !slices.Contains(envelope.Analysis.Skipped, name+" (excluded)") {
					t.Errorf("missing exclusion receipt for %s: %+v", name, envelope.Analysis)
				}
			}
			if !slices.Equal(cfg.Exclude, original) || !slices.Equal(deps.App.EffectiveConfig().Config.Exclude, config.NormalizeExcludes(original)) {
				t.Fatal("invocation mutated configured exclusions")
			}
		})
	}
}

func TestExcludePathRejectsInvalidScope(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", ".", "/", "../outside", "a/../../outside"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			deps := newTestDeps(&out)
			err := cli.Run(t.Context(), []string{"--exclude-path=" + value, "config", "print"}, deps)
			if err == nil || !strings.Contains(err.Error(), "--exclude-path") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestExcludePathDoesNotShadowReviewExclude(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	deps := newTestDeps(&out)
	if err := cli.Run(t.Context(), []string{"--exclude-path", "apps", "review", "report", "--help"}, deps); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--exclude=", "--exclude-path="} {
		if !strings.Contains(out.String(), flag) {
			t.Errorf("help missing %s: %s", flag, out.String())
		}
	}
}
