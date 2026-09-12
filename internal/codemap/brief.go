package codemap

import (
	"sort"

	"github.com/dotcommander/pan/internal/analyze"
)

// SymbolFamilies returns a compact count of symbol kinds in a file. It is
// intentionally brief-only evidence: the full symbol list remains available
// from context map and symbol queries.
func SymbolFamilies(symbols []analyze.Symbol) map[string]int {
	out := make(map[string]int)
	for _, symbol := range symbols {
		kind := symbol.Kind
		if kind == "" {
			kind = "unknown"
		}
		out[kind]++
	}
	return out
}

// SortedSymbolFamilies provides deterministic family rows for text renderers.
func SortedSymbolFamilies(symbols []analyze.Symbol) []string {
	families := SymbolFamilies(symbols)
	keys := make([]string, 0, len(families))
	for key := range families {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key)
	}
	return out
}
