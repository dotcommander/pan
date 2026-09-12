package scan

import (
	"context"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestOrphansWithReferencesPrefersSemanticResult(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{SchemaVersion: analyze.SchemaVersion, Symbols: []analyze.Symbol{{Name: "Unused", Kind: "function", Exported: true, Package: "example", Location: analyze.Location{Path: "source.go", Line: 1}}}}
	report, err := OrphansWithReferences(context.Background(), snap, 0, func(_ context.Context, candidate OrphanCandidate) (ReferenceResult, error) {
		if candidate.Name != "Unused" {
			t.Fatalf("candidate = %+v", candidate)
		}
		return ReferenceResult{Available: true, NonTest: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ZeroRefs) != 0 || len(report.TestOnlyRefs) != 0 {
		t.Fatalf("semantic reference must suppress orphan candidate: %+v", report)
	}
}

func TestOrphansWithReferencesClassifiesTestOnly(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{SchemaVersion: analyze.SchemaVersion, Symbols: []analyze.Symbol{{Name: "Tested", Kind: "function", Exported: true, Package: "example", Location: analyze.Location{Path: "source.go", Line: 1}}}}
	report, err := OrphansWithReferences(context.Background(), snap, 0, func(context.Context, OrphanCandidate) (ReferenceResult, error) {
		return ReferenceResult{Available: true, Test: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.TestOnlyRefs) != 1 || report.TestOnlyRefs[0].Name != "Tested" {
		t.Fatalf("test-only references = %+v", report.TestOnlyRefs)
	}
}
