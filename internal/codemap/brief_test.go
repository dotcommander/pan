package codemap

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestSymbolFamilies(t *testing.T) {
	symbols := []analyze.Symbol{{Kind: "function"}, {Kind: "function"}, {Kind: "type"}, {Kind: ""}}
	got := SymbolFamilies(symbols)
	if got["function"] != 2 || got["type"] != 1 || got["unknown"] != 1 {
		t.Fatalf("families = %#v", got)
	}
}
