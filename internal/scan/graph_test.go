package scan

import (
	"context"
	"reflect"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func graphFixture() analyze.Snapshot {
	return analyze.Snapshot{
		Edges: []analyze.Edge{
			{From: "a", To: "b", Kind: "calls", Confidence: analyze.ConfidenceConfirmed},
			{From: "a", To: "b", Kind: "calls", Confidence: analyze.ConfidenceConfirmed},
			{From: "b", To: "c", Kind: "calls", Confidence: analyze.ConfidenceConfirmed},
			{From: "c", To: "a", Kind: "calls", Confidence: analyze.ConfidenceConfirmed},
			{From: "d", To: "d", Kind: "calls", Confidence: analyze.ConfidenceConfirmed},
			{From: "e", To: "a", Kind: "imports", Confidence: analyze.ConfidenceConfirmed},
		},
	}
}

func TestGraphDeterministicStructure(t *testing.T) {
	t.Parallel()
	first, err := Graph(context.Background(), graphFixture(), 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Graph(context.Background(), graphFixture(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("equal inputs must produce deeply equal reports:\n%+v\n%+v", first, second)
	}
	if first.Nodes != 5 {
		t.Fatalf("nodes = %d, want 5", first.Nodes)
	}
	if first.Edges != 5 {
		t.Fatalf("edges = %d, want 5 unique directed pairs (duplicate a->b collapses)", first.Edges)
	}
	wantKinds := map[string]int{"calls": 5, "imports": 1}
	if !reflect.DeepEqual(first.Kinds, wantKinds) {
		t.Fatalf("kinds = %v, want %v", first.Kinds, wantKinds)
	}
	wantHubs := []struct {
		id            string
		inDeg, outDeg int
	}{{"a", 2, 1}, {"b", 1, 1}, {"c", 1, 1}, {"d", 1, 1}, {"e", 0, 1}}
	for i, want := range wantHubs {
		hub := first.Hubs[i]
		if hub.ID != want.id || hub.InDegree != want.inDeg || hub.OutDegree != want.outDeg {
			t.Fatalf("hub[%d] = %+v, want %s in=%d out=%d", i, hub, want.id, want.inDeg, want.outDeg)
		}
	}
	wantCycles := [][]string{{"a", "b", "c"}, {"d"}}
	if !reflect.DeepEqual(first.Cycles, wantCycles) {
		t.Fatalf("cycles = %v, want %v (sorted members, recursion self-loop included)", first.Cycles, wantCycles)
	}
	if first.Truncations != nil {
		t.Fatalf("untruncated report must omit truncations, got %v", first.Truncations)
	}
}

func TestGraphTopTruncatesHubs(t *testing.T) {
	t.Parallel()
	report, err := Graph(context.Background(), graphFixture(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Hubs) != 2 || report.Hubs[0].ID != "a" || report.Hubs[1].ID != "b" {
		t.Fatalf("hubs = %+v, want top 2 by degree [a b]", report.Hubs)
	}
	if len(report.Truncations) != 1 || report.Truncations[0].Field != "hubs" ||
		report.Truncations[0].Shown != 2 || report.Truncations[0].Total != 5 {
		t.Fatalf("truncations = %+v, want one hubs entry shown=2 total=5", report.Truncations)
	}
}

func TestGraphDefaultTopWhenUnset(t *testing.T) {
	t.Parallel()
	report, err := Graph(context.Background(), graphFixture(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Hubs) != 5 {
		t.Fatalf("five nodes must all fit under the default top, got %d", len(report.Hubs))
	}
}

func TestGraphCapsCycles(t *testing.T) {
	t.Parallel()
	edges := make([]analyze.Edge, 0, 120)
	for i := 0; i < 60; i++ {
		from, to := numberNode(i), numberNode(i+60)
		edges = append(edges,
			analyze.Edge{From: from, To: to, Kind: "calls"},
			analyze.Edge{From: to, To: from, Kind: "calls"},
		)
	}
	report, err := Graph(context.Background(), analyze.Snapshot{Edges: edges}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cycles) != graphMaxCycles {
		t.Fatalf("cycles = %d, want capped at %d", len(report.Cycles), graphMaxCycles)
	}
	var cycleTruncation bool
	for _, truncation := range report.Truncations {
		if truncation.Field == "cycles" && truncation.Shown == graphMaxCycles && truncation.Total == 60 {
			cycleTruncation = true
		}
	}
	if !cycleTruncation {
		t.Fatalf("truncations = %+v, want a cycles entry shown=%d total=60", report.Truncations, graphMaxCycles)
	}
}

func TestGraphEmptySnapshotHasStableShape(t *testing.T) {
	t.Parallel()
	report, err := Graph(context.Background(), analyze.Snapshot{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Nodes != 0 || report.Edges != 0 || report.Hubs == nil || report.Cycles == nil {
		t.Fatalf("empty snapshot must keep non-nil collections: %+v", report)
	}
}

func numberNode(i int) string {
	return "n" + string(rune('A'+i%26)) + string(rune('0'+i/26))
}
