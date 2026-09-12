package retrieval

import (
	"context"
	"fmt"
	"slices"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// maxCallees bounds the handler callee list.
const maxCallees = 10

// EndpointContext is the vertical-slice bundle for one resolved route: the
// registration, its handler symbol, the handler's lexical callees, the tests
// that plausibly touch it, and the handler file's blast radius.
type EndpointContext struct {
	Route       RouteRegistration    `json:"route"`
	Handler     *SymbolMatch         `json:"handler,omitempty"`
	Ambiguous   []RouteRegistration  `json:"ambiguous_routes,omitempty"`
	Callees     []string             `json:"callees,omitempty"`
	Tests       []string             `json:"tests,omitempty"`
	Impact      ImpactResult         `json:"impact"`
	Truncations []analyze.Truncation `json:"truncations,omitempty"`
}

// Endpoint resolves one route query into its vertical slice. Zero matching
// routes is an error; extra exact matches surface as capped ambiguous
// alternatives. Callers receive lexical evidence labels throughout: route
// detection, callee matching, and handler resolution are all by name.
func Endpoint(ctx context.Context, snap analyze.Snapshot, ranked []ranking.RankedFile, query string) (EndpointContext, error) {
	routes, err := Routes(ctx, snap)
	if err != nil {
		return EndpointContext{}, err
	}
	matches := matchRoutes(routes, query)
	if len(matches) == 0 {
		return EndpointContext{}, fmt.Errorf("no route matching %q", query)
	}
	out := EndpointContext{Route: matches[0]}
	if len(matches) > 1 {
		rest := matches[1:]
		if len(rest) > maxAmbiguousMatches {
			out.Truncations = append(out.Truncations, analyze.Truncation{
				Field:  "ambiguous_routes",
				Shown:  maxAmbiguousMatches,
				Total:  len(rest),
				Reason: "ambiguity cap",
			})
			rest = rest[:maxAmbiguousMatches]
		}
		out.Ambiguous = rest
	}

	if handlerMatches := Find(ranked, out.Route.Handler, "function", ""); len(handlerMatches) > 0 {
		handler := handlerMatches[0]
		// Prefer a handler defined in the registering file when name
		// resolution is ambiguous across packages.
		for _, candidate := range handlerMatches {
			if candidate.File == out.Route.File {
				handler = candidate
				break
			}
		}
		out.Handler = &handler
		callees, truncation := handlerCallees(snap, handler)
		out.Callees = callees
		if truncation != nil {
			out.Truncations = append(out.Truncations, *truncation)
		}
		impact, _ := Impact(ranked, handler.File)
		out.Impact = impact
		out.Tests = impactTests(handler.File, ranked)
		return out, nil
	}

	// Handler unresolved (inline closure or name absent from the snapshot):
	// fall back to the registering file's blast radius.
	impact, _ := Impact(ranked, out.Route.File)
	out.Impact = impact
	return out, nil
}

// handlerCallees lists the distinct callee names recorded from the handler's
// call edges, capped at maxCallees with a truncation record. Edges key on
// bare symbol names, so callees carry lexical evidence semantics: a
// same-named helper elsewhere is indistinguishable.
func handlerCallees(snap analyze.Snapshot, handler SymbolMatch) ([]string, *analyze.Truncation) {
	seen := make(map[string]struct{})
	var callees []string
	for _, edge := range snap.Edges {
		if edge.Kind != edgeKindCalls || edge.From != handler.Symbol.Name {
			continue
		}
		if edge.Location.Path != handler.File {
			continue
		}
		if _, dup := seen[edge.To]; dup {
			continue
		}
		seen[edge.To] = struct{}{}
		callees = append(callees, edge.To)
	}
	slices.Sort(callees)
	if len(callees) <= maxCallees {
		return callees, nil
	}
	total := len(callees)
	return callees[:maxCallees], &analyze.Truncation{
		Field:  "callees",
		Shown:  maxCallees,
		Total:  total,
		Reason: "callee cap",
	}
}
