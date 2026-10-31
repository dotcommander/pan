package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestReviewSourcesPreservesStandalonePackets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                       "module example.com/project\n",
		"cmd/api/main.go":              "package main\nfunc main() { os.WriteFile(\"out\", nil, 0600) }\n",
		"internal/api/handler.go":      "package api\n// TODO inspect\nfunc Start() { panic(\"oops\") }\n",
		"internal/api/handler_test.go": "package api\nfunc TestStart() {}\n",
		"internal/plain.go":            "package internal\n",
		"src/main.py":                  "# TODO implement\nprint('hello')\n",
	})
	snap := goSnapshot(root, "cmd/api/main.go", "internal/api/handler.go", "internal/api/handler_test.go", "internal/plain.go")
	snap.Files = append(snap.Files, analyze.File{Path: "src/main.py", Language: analyze.LanguagePython})
	for _, tc := range []struct {
		name    string
		options ReviewSourceOptions
	}{
		{name: "default"},
		{name: "bounded", options: ReviewSourceOptions{Limit: 1, Language: analyze.LanguagePython}},
		{name: "custom", options: ReviewSourceOptions{Limit: 1, Patterns: &CustomPatterns{PathTerms: []PathTerm{{Term: "plain", Weight: 7}}, Content: []ContentPattern{{ID: "marker", Pattern: "TODO", Weight: 2, MaxMatches: 2}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			got, err := ReviewSources(ctx, snap, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			var risks RiskReport
			if tc.options.Patterns == nil {
				risks, err = Risk(ctx, snap, tc.options.Limit)
			} else {
				risks, err = RiskWithCustomPatterns(ctx, snap, tc.options.Limit, tc.options.Patterns)
			}
			if err != nil {
				t.Fatal(err)
			}
			surface, err := Surface(ctx, snap, tc.options.Limit)
			if err != nil {
				t.Fatal(err)
			}
			effects, err := EffectsWithOptions(ctx, snap, EffectsOptions{Limit: tc.options.Limit, Language: tc.options.Language})
			if err != nil {
				t.Fatal(err)
			}
			want := ReviewSourceReports{Risks: risks, Surface: surface, Effects: effects}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("shared packet differs from independent packets:\nshared=%+v\nindependent=%+v", got, want)
			}
		})
	}
}

func TestReviewSourcesRetainsTruncationAndReadErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "cmd", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("// TODO\n", maxScanLines+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := goSnapshot(root, "cmd/main.go")
	ctx := t.Context()
	got, err := ReviewSources(ctx, snap, ReviewSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	risks, err := Risk(ctx, snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := Surface(ctx, snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := Effects(ctx, snap, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, ReviewSourceReports{Risks: risks, Surface: surface, Effects: effects}) {
		t.Fatal("bounded packet or truncation diverged")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReviewSources(ctx, snap, ReviewSourceOptions{}); err == nil || !strings.Contains(err.Error(), "scan risk source cmd/main.go") {
		t.Fatalf("missing source error = %v", err)
	}
}

func TestRiskModuleResolutionIsRequestLocal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"go.mod": "module example.com/project\n"})
	snap := goSnapshot(root, "internal/api/a.go")
	signals := structuralSignals(snap)
	if signals.module != "example.com/project" {
		t.Fatalf("module = %q", signals.module)
	}
	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if got := packageImportPathForModule(signals.module, "internal/api/a.go"); got != "example.com/project/internal/api" {
		t.Fatalf("request-local import path = %q", got)
	}
}
