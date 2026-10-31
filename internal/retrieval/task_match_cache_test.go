package retrieval

import (
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestTaskCandidateCachedMatchesPreserveCoverage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, goal string
		terms      []string
		ranked     []ranking.RankedFile
		want       map[string][]string
		fallback   bool
	}{
		{
			name: "exact and prefix", goal: "author", terms: []string{"author"},
			ranked: []ranking.RankedFile{
				{Path: "one.go", Symbols: []analyze.Symbol{{Name: "Author"}}},
				{Path: "two.go", Symbols: []analyze.Symbol{{Name: "Authorizer"}}},
			},
			want: map[string][]string{"one.go": {"author"}, "two.go": {"author"}},
		},
		{
			name: "symbol mode keeps exact identifier priority", goal: "Author", terms: []string{"author"},
			ranked: []ranking.RankedFile{
				{Path: "one.go", Symbols: []analyze.Symbol{{Name: "Author"}}},
				{Path: "two.go", Symbols: []analyze.Symbol{{Name: "Authorizer"}}},
			},
			want: map[string][]string{"one.go": {"author"}, "two.go": {}},
		},
		{
			name: "explicit identifier absent from expanded terms", goal: "Inspect `_x` in request parsing.", terms: []string{"_x"},
			ranked: []ranking.RankedFile{
				{Path: "request/owner.go", Symbols: []analyze.Symbol{{Name: "_x"}}},
				{Path: "request/decoy.go", Symbols: []analyze.Symbol{{Name: "_xyz"}}},
			},
			want: map[string][]string{"request/owner.go": {"_x"}, "request/decoy.go": {}},
		},
		{
			name: "structural importer has no direct terms", goal: "author", terms: []string{"author"},
			ranked: []ranking.RankedFile{
				{Path: "pkg/owner.go", Symbols: []analyze.Symbol{{Name: "Author"}}},
				{Path: "app/main.go", InternalImports: []string{"pkg"}},
			},
			want: map[string][]string{"pkg/owner.go": {"author"}, "app/main.go": {}},
		},
		{
			name: "fallback never supports terms", goal: "unmatched", terms: []string{"unmatched"},
			ranked: []ranking.RankedFile{{Path: "one.go"}, {Path: "two.go"}},
			want:   map[string][]string{"one.go": {}, "two.go": {}}, fallback: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			identifiers := taskGoalIdentifiers(tc.goal)
			candidates := taskCandidates(tc.ranked, analyze.Snapshot{}, tc.goal)
			if len(candidates) != len(tc.want) {
				t.Fatalf("candidate count = %d, want %d", len(candidates), len(tc.want))
			}
			for _, candidate := range candidates {
				want, ok := tc.want[candidate.file.Path]
				if !ok {
					t.Fatalf("unexpected candidate %s", candidate.file.Path)
				}
				if candidate.fallback != tc.fallback {
					t.Fatalf("%s fallback = %t", candidate.file.Path, candidate.fallback)
				}
				got := candidateTerms(candidate, tc.terms, identifiers)
				if !slices.Equal(got, want) {
					t.Fatalf("%s matched %q, want %q", candidate.file.Path, got, want)
				}
			}
		})
	}
}
