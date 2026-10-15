package ranking_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/retrieval"
)

func TestRankWiresGoPackageImports(t *testing.T) {
	t.Parallel()
	snapshot := importSnapshot()
	snapshot.Edges = append(snapshot.Edges,
		importEdge("internal/cli/clean_output.go", "example.test/project"),
		importEdge("internal/cli/clean_output.go", "sort"),
		importEdge("internal/cli/clean_output.go", "example.test/project/internal/missing"),
		importEdge("internal/cli/clean_output.go", "example.test/foreign/internal/clean"),
		importEdge("internal/cli/clean_output.go", "example.test/project-extra/internal/clean"),
		importEdge("other/client.go", "example.test/foreign/internal/clean"),
		importEdge("notes.md", "example.test/project/internal/clean"),
	)
	ranked := ranking.Rank(snapshot, "example.test/project", ranking.Options{})
	cli := findRanked(t, ranked, "internal/cli/clean_output.go")
	if !slices.Equal(cli.InternalImports, []string{".", "internal/clean"}) || cli.DependsOn != 2 {
		t.Fatalf("CLI dependencies = %v (%d), want root and clean only", cli.InternalImports, cli.DependsOn)
	}
	for _, file := range []string{"internal/clean/coverage.go", "internal/clean/other.go"} {
		clean := findRanked(t, ranked, file)
		if clean.ImportedBy != 2 || clean.Components[ranking.ComponentImports] != 20 {
			t.Fatalf("%s import evidence = %+v, want two unique importers and score 20", file, clean)
		}
	}
	root := findRanked(t, ranked, "root.go")
	if root.ImportedBy != 1 || root.Components[ranking.ComponentImports] != 10 {
		t.Fatalf("root import evidence = %+v, want one importer and score 10", root)
	}
	for _, file := range []string{"other/client.go", "notes.md"} {
		if got := findRanked(t, ranked, file); len(got.InternalImports) != 0 || got.DependsOn != 0 {
			t.Fatalf("non-internal/non-Go imports leaked for %s: %+v", file, got)
		}
	}
	if !reflect.DeepEqual(ranked, ranking.Rank(snapshot, "example.test/project", ranking.Options{})) {
		t.Fatal("repeated ranking changed import evidence or ordering")
	}
}

func TestRankResolvesGoImportsWithoutModule(t *testing.T) {
	t.Parallel()
	ranked := ranking.Rank(importSnapshot(), "", ranking.Options{})
	cli := findRanked(t, ranked, "internal/cli/clean_output.go")
	if !slices.Equal(cli.InternalImports, []string{"internal/clean"}) || cli.DependsOn != 1 {
		t.Fatalf("fallback dependencies = %+v, want clean only", cli)
	}
	clean := findRanked(t, ranked, "internal/clean/coverage.go")
	if clean.ImportedBy != 2 || clean.Components[ranking.ComponentImports] != 20 {
		t.Fatalf("fallback import evidence = %+v, want two unique importers and score 20", clean)
	}
}

func TestRankImpactIncludesGoPackageConsumers(t *testing.T) {
	t.Parallel()
	ranked := ranking.Rank(importSnapshot(), "example.test/project", ranking.Options{})
	impact, ok := retrieval.Impact(ranked, "internal/clean/coverage.go")
	if !ok {
		t.Fatal("coverage file not found")
	}
	importers := slices.Clone(impact.ImportedBy)
	slices.Sort(importers)
	if !slices.Equal(importers, []string{"internal/app/clean.go", "internal/cli/clean_output.go"}) {
		t.Fatalf("impact importers = %v, want app and CLI", importers)
	}
	if !slices.Equal(impact.AffectedPackages, []string{"app", "clean", "cli"}) {
		t.Fatalf("affected packages = %v, want app, clean, cli", impact.AffectedPackages)
	}
	if !slices.Equal(impact.Tests, []string{"internal/clean/coverage_test.go"}) ||
		!slices.Equal(impact.LikelyTestCommands, []string{"go test ./internal/clean"}) {
		t.Fatalf("cleanup test guidance = %+v", impact)
	}
	if impact.Evidence["imported_by"] != analyze.ConfidenceSyntactic {
		t.Fatalf("import confidence = %q, want syntactic", impact.Evidence["imported_by"])
	}
}

func importSnapshot() analyze.Snapshot {
	snapshot := analyze.Snapshot{}
	for _, file := range []struct{ path, pkg string }{
		{"root.go", "project"},
		{"internal/clean/coverage.go", "clean"},
		{"internal/clean/other.go", "clean"},
		{"internal/clean/coverage_test.go", "clean"},
		{"internal/cli/clean_output.go", "cli"},
		{"internal/app/clean.go", "app"},
		{"other/client.go", "other"},
	} {
		snapshot.Files = append(snapshot.Files, analyze.File{Path: file.path, Language: analyze.LanguageGo})
		snapshot.Symbols = append(snapshot.Symbols, analyze.Symbol{
			Name: "Fixture", Kind: "struct", Package: file.pkg, Exported: true,
			Location: analyze.Location{Path: file.path, Line: 1}, EndLine: 1,
		})
	}
	snapshot.Files = append(snapshot.Files, analyze.File{Path: "notes.md", Language: "unknown"})
	snapshot.Edges = []analyze.Edge{
		importEdge("internal/cli/clean_output.go", "example.test/project/internal/clean"),
		importEdge("internal/cli/clean_output.go", "example.test/project/internal/clean"),
		importEdge("internal/app/clean.go", "example.test/project/internal/clean"),
	}
	return snapshot
}

func importEdge(from, to string) analyze.Edge {
	return analyze.Edge{From: from, To: to, Kind: "imports"}
}

func findRanked(t *testing.T, ranked []ranking.RankedFile, file string) ranking.RankedFile {
	t.Helper()
	for _, candidate := range ranked {
		if candidate.Path == file {
			return candidate
		}
	}
	t.Fatalf("ranked file %s not found", file)
	return ranking.RankedFile{}
}
