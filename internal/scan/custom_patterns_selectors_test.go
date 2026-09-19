package scan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestCustomPatternSelectorsScopeRulesToPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, rel := range []string{"cmd/worker.go", "internal/handler.go"} {
		name := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("package demo\n// TODO: review\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	patterns, err := LoadCustomPatterns([]byte(`{
		"version": 1,
		"path_terms": [{"term": "handler", "weight": 3, "include_globs": ["internal/**"]}],
		"content_patterns": [{"id": "todo", "pattern": "TODO", "weight": 5, "max_matches": 1, "exclude_globs": ["cmd/**"]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	report, err := RiskWithCustomPatterns(context.Background(), analyze.Snapshot{Root: root, Files: []analyze.File{
		{Path: "cmd/worker.go", Language: analyze.LanguageGo},
		{Path: "internal/handler.go", Language: analyze.LanguageGo},
	}}, 0, &patterns)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileRisk{}
	for _, risk := range report.Files {
		byPath[risk.Path] = risk
	}
	included, found := byPath["internal/handler.go"]
	if !found {
		t.Fatalf("internal/handler.go missing from report: %#v", report.Files)
	}
	if !slices.Contains(included.Reasons, "custom:path:handler") || !slices.Contains(included.Reasons, "custom:content:todo:1") {
		t.Fatalf("selector-matched file missing custom reasons: %#v", included.Reasons)
	}
	excluded, found := byPath["cmd/worker.go"]
	if !found {
		t.Fatalf("cmd/worker.go missing from report: %#v", report.Files)
	}
	if slices.ContainsFunc(excluded.Reasons, func(reason string) bool { return strings.HasPrefix(reason, "custom:") }) {
		t.Fatalf("excluded file must not carry custom reasons: %#v", excluded.Reasons)
	}
}

func TestLoadCustomPatternsRejectsBadVersionAndSelectors(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"version":2,"path_terms":[{"term":"x","weight":1}]}`,
		`{"version":1,"path_terms":[{"term":"x","weight":1,"include_globs":["["]}]}`,
		`{"path_terms":[{"term":"x","weight":1,"exclude_globs":["  "]}]}`,
		`{"content_patterns":[{"id":"x","pattern":"x","weight":1,"max_matches":1,"exclude_globs":["a["]}]}`,
	}
	for _, catalog := range cases {
		if _, err := LoadCustomPatterns([]byte(catalog)); err == nil {
			t.Errorf("catalog accepted: %s", catalog)
		}
	}
	valid := `{"version":1,"path_terms":[{"term":"x","weight":1,"include_globs":["internal/**"],"exclude_globs":["**/*_test.go"]}]}`
	if _, err := LoadCustomPatterns([]byte(valid)); err != nil {
		t.Fatalf("valid selector catalog rejected: %v", err)
	}
	legacy := `{"path_terms":[{"term":"x","weight":1}]}`
	if _, err := LoadCustomPatterns([]byte(legacy)); err != nil {
		t.Fatalf("legacy unversioned catalog rejected: %v", err)
	}
}
