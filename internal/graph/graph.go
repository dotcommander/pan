// Package graph builds deterministic views over directed evidence graphs.
// All operations are pure functions of their inputs: equal edge sets always
// produce deeply equal results, and every returned collection is sorted so
// rendering is byte-stable.
package graph

import (
	"slices"
	"sort"
	"strings"
)

// Edge is one directed edge in an evidence graph.
type Edge struct {
	From string
	To   string
}

// Hub is one node with its degree evidence.
type Hub struct {
	ID        string `json:"id"`
	InDegree  int    `json:"in_degree"`
	OutDegree int    `json:"out_degree"`
}

// Directed is a deduplicated directed graph. The zero value is an empty
// graph ready for AddEdge.
type Directed struct {
	out map[string]map[string]struct{}
	in  map[string]map[string]struct{}
}

// NewDirected returns an empty directed graph.
func NewDirected() *Directed {
	return &Directed{
		out: make(map[string]map[string]struct{}),
		in:  make(map[string]map[string]struct{}),
	}
}

// AddEdge records from -> to. Duplicate edges are ignored; self-loops are
// kept because they are meaningful evidence (for example recursion).
func (g *Directed) AddEdge(from, to string) {
	if g.out[from] == nil {
		g.out[from] = make(map[string]struct{})
	}
	g.out[from][to] = struct{}{}
	if g.in[to] == nil {
		g.in[to] = make(map[string]struct{})
	}
	g.in[to][from] = struct{}{}
}

// Nodes returns every node with at least one incident edge, sorted.
func (g *Directed) Nodes() []string {
	nodes := make([]string, 0, len(g.out)+len(g.in))
	for node := range g.out {
		nodes = append(nodes, node)
	}
	for node := range g.in {
		if len(g.out[node]) == 0 {
			nodes = append(nodes, node)
		}
	}
	slices.Sort(nodes)
	return nodes
}

// EdgeCount returns the number of unique directed edges.
func (g *Directed) EdgeCount() int {
	total := 0
	for _, targets := range g.out {
		total += len(targets)
	}
	return total
}

// InDegree returns the number of distinct predecessors of id.
func (g *Directed) InDegree(id string) int { return len(g.in[id]) }

// OutDegree returns the number of distinct successors of id.
func (g *Directed) OutDegree(id string) int { return len(g.out[id]) }

// Hubs returns the top nodes ranked by total degree (descending), breaking
// ties by ascending id. top <= 0 returns every node.
func (g *Directed) Hubs(top int) []Hub {
	nodes := g.Nodes()
	hubs := make([]Hub, 0, len(nodes))
	for _, node := range nodes {
		hubs = append(hubs, Hub{ID: node, InDegree: g.InDegree(node), OutDegree: g.OutDegree(node)})
	}
	sort.SliceStable(hubs, func(i, j int) bool {
		ti := hubs[i].InDegree + hubs[i].OutDegree
		tj := hubs[j].InDegree + hubs[j].OutDegree
		if ti != tj {
			return ti > tj
		}
		return hubs[i].ID < hubs[j].ID
	})
	if top > 0 && len(hubs) > top {
		hubs = hubs[:top]
	}
	return hubs
}

// Cycles returns the nontrivial strongly connected components: components
// with more than one node, or a single node with a self-loop. Components
// are internally sorted and the component list itself is sorted, so output
// is deterministic.
func (g *Directed) Cycles() [][]string {
	var cycles [][]string
	for _, component := range g.StronglyConnected() {
		trivial := len(component) == 1 && !g.hasSelfLoop(component[0])
		if !trivial {
			cycles = append(cycles, component)
		}
	}
	return cycles
}

func (g *Directed) hasSelfLoop(id string) bool {
	_, loop := g.out[id][id]
	return loop
}

// StronglyConnected returns every strongly connected component using
// Tarjan's algorithm. Node iteration and adjacency traversal follow sorted
// order so the result is deterministic. Components of one node without a
// self-loop are included; use Cycles for cycle evidence only.
func (g *Directed) StronglyConnected() [][]string {
	nodes := g.Nodes()
	t := &tarjan{
		out:     g.out,
		index:   make(map[string]int, len(nodes)),
		low:     make(map[string]int, len(nodes)),
		onStack: make(map[string]bool, len(nodes)),
	}
	for _, node := range nodes {
		if _, seen := t.index[node]; !seen {
			t.strongConnect(node)
		}
	}
	// Members of each component are appended in completion order; sort them
	// and then sort the component list for deterministic output.
	for _, component := range t.components {
		slices.Sort(component)
	}
	slices.SortFunc(t.components, func(a, b []string) int {
		return strings.Compare(a[0], b[0])
	})
	return t.components
}

// tarjan carries the iterative Tarjan state. Recursion depth is bounded by
// the number of nodes, which analysis bounds cap up front.
type tarjan struct {
	out        map[string]map[string]struct{}
	index      map[string]int
	low        map[string]int
	onStack    map[string]bool
	stack      []string
	next       int
	components [][]string
}

func (t *tarjan) strongConnect(v string) {
	t.index[v] = t.next
	t.low[v] = t.next
	t.next++
	t.stack = append(t.stack, v)
	t.onStack[v] = true

	successors := make([]string, 0, len(t.out[v]))
	for successor := range t.out[v] {
		successors = append(successors, successor)
	}
	slices.Sort(successors)
	for _, w := range successors {
		if _, seen := t.index[w]; !seen {
			t.strongConnect(w)
			t.low[v] = min(t.low[v], t.low[w])
		} else if t.onStack[w] {
			t.low[v] = min(t.low[v], t.index[w])
		}
	}

	if t.low[v] == t.index[v] {
		var component []string
		for {
			w := t.stack[len(t.stack)-1]
			t.stack = t.stack[:len(t.stack)-1]
			t.onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		t.components = append(t.components, component)
	}
}
