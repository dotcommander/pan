package ranking

import (
	"github.com/dotcommander/pan/internal/analyze"
	"reflect"
	"testing"
)

func TestResolveSuffixSelectsLongestDirectory(t *testing.T) {
	t.Parallel()
	for i := 0; i < 100; i++ {
		dirs := map[string]struct{}{"util": {}, "internal/util": {}, "pkg/internal/util": {}}
		var got string
		if !resolveSuffix("example.org/pkg/internal/util", dirs, &got) || got != "pkg/internal/util" {
			t.Fatalf("resolved %q", got)
		}
	}
}

func TestReferenceGraphAccumulationIsDeterministic(t *testing.T) {
	t.Parallel()
	counts := map[referenceGraphPair]int{}
	ambiguity := map[referenceGraphPair]int{}
	for i := 1; i < 100; i++ {
		p := referenceGraphPair{0, i}
		counts[p] = i
		ambiguity[p] = i%7 + 1
	}
	_, _, want := referenceGraphWeights(counts, ambiguity, 100)
	for i := 0; i < 100; i++ {
		_, _, got := referenceGraphWeights(counts, ambiguity, 100)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("nondeterministic sums: %v != %v", got, want)
		}
	}
}

func TestReferenceBonusRefusesMissingCaptureWithoutMutation(t *testing.T) {
	t.Parallel()
	ranked := []RankedFile{{Path: "owner.ts", Language: analyze.LanguageTypescript, Symbols: []analyze.Symbol{{Name: "Widget", Exported: true}}, Components: map[string]int{}}, {Path: "use.ts", Language: analyze.LanguageTypescript, Components: map[string]int{}}}
	snap := analyze.Snapshot{Captured: map[string][]byte{"owner.ts": []byte("export class Widget {}")}}
	if err := ApplySymbolReferenceBonus(snap, ranked); err == nil {
		t.Fatal("missing capture should fail")
	}
	if ranked[0].Score != 0 {
		t.Fatal("partial scoring escaped failed inspection")
	}
	snap.Captured["use.ts"] = []byte("new Widget()")
	if err := ApplySymbolReferenceBonus(snap, ranked); err != nil {
		t.Fatal(err)
	}
	if ranked[0].Components["symbol_refs"] != 2 {
		t.Fatalf("bonus %#v", ranked)
	}
}
