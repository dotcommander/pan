package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestRiskWithCustomPatternsAddsBoundedRegexMatches(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	name := filepath.Join(root, "service.go")
	if err := os.WriteFile(name, []byte("package demo\n// TODO: review\n// TODO: review\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patterns, err := LoadCustomPatterns([]byte(`{"path_terms":[{"term":"service","weight":3}],"content_patterns":[{"id":"todo","pattern":"TODO","weight":5,"max_matches":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	report, err := RiskWithCustomPatterns(context.Background(), analyze.Snapshot{Root: root, Files: []analyze.File{{Path: "service.go", Language: languageGo}}}, 0, &patterns)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0].Score != 14 {
		t.Fatalf("custom score = %#v, want one score-14 file", report.Files)
	}
	if got := report.Files[0].Reasons; len(got) != 4 || got[2] != "custom:path:service" || got[3] != "custom:content:todo:1" {
		t.Fatalf("reasons = %#v", got)
	}
}

func TestLoadCustomPatternsRejectsInvalidCatalog(t *testing.T) {
	t.Parallel()
	if _, err := LoadCustomPatterns([]byte(`{"content_patterns":[{"id":"bad","pattern":"(","weight":1,"max_matches":1}]}`)); err != nil {
		t.Fatalf("validation should defer regex compilation, got %v", err)
	}
	if _, err := LoadCustomPatterns([]byte(`{"path_terms":[{"term":"","weight":1}]}`)); err == nil {
		t.Fatal("empty path term accepted")
	}
}
