package scan

import (
	"context"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/graph"
)

const (
	graphDefaultTop = 25
	graphMaxCycles  = 50
	graphHubField   = "hubs"
	graphCycleField = "cycles"
)

// GraphReport is the deterministic structural view over the snapshot's
// directed evidence graph: unique directed pairs, degree-ranked hub nodes,
// and nontrivial strongly connected components (cycles, including
// single-node recursion).
type GraphReport struct {
	Nodes       int            `json:"nodes"`
	Edges       int            `json:"edges"`
	Kinds       map[string]int `json:"kinds"`
	Hubs        []graph.Hub    `json:"hubs"`
	Cycles      [][]string     `json:"cycles"`
	Truncations []Truncation   `json:"truncations,omitempty"`
}

// Graph builds the report from every edge in the snapshot regardless of
// confidence; callers scope the snapshot. Duplicate endpoint pairs collapse
// into one adjacency entry, so Edges counts unique directed pairs and hub
// degrees count distinct neighbors. Ordering comes from the graph package's
// deterministic guarantees. A top <= 0 uses the bounded default.
func Graph(_ context.Context, snap analyze.Snapshot, top int) (GraphReport, error) {
	directed := graph.NewDirected()
	kinds := make(map[string]int)
	for _, edge := range snap.Edges {
		directed.AddEdge(edge.From, edge.To)
		kinds[edge.Kind]++
	}
	if top <= 0 {
		top = graphDefaultTop
	}
	nodes := directed.Nodes()
	hubs := directed.Hubs(top)
	cycles := directed.Cycles()
	report := GraphReport{
		Nodes:  len(nodes),
		Edges:  directed.EdgeCount(),
		Kinds:  kinds,
		Hubs:   hubs,
		Cycles: cycles,
	}
	if report.Cycles == nil {
		report.Cycles = [][]string{}
	}
	if len(hubs) < len(nodes) {
		report.Truncations = append(report.Truncations, Truncation{
			Field:  graphHubField,
			Shown:  len(hubs),
			Total:  len(nodes),
			Reason: "bounded top hubs by total degree",
		})
	}
	if len(cycles) > graphMaxCycles {
		report.Truncations = append(report.Truncations, Truncation{
			Field:  graphCycleField,
			Shown:  graphMaxCycles,
			Total:  len(cycles),
			Reason: "bounded cycle list",
		})
		report.Cycles = cycles[:graphMaxCycles]
	}
	return report, nil
}
