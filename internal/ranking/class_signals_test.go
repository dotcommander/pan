package ranking

import (
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestIntentMatchingRequiresTermPartsNotSubstrings(t *testing.T) {
	t.Parallel()
	// 'auth' must boost the exact part match (internal/auth.go) strictly
	// above the prefix-overlap match (author.go), and mid-word substrings
	// must not boost anything at all. Orientation and depth components are
	// outside this contract, so compare the intent component directly.
	snapshot := analyze.Snapshot{Files: []analyze.File{
		{Path: "author.go", Language: "go"},
		{Path: "internal/auth.go", Language: "go"},
		{Path: "loiter.go", Language: "go"},
	}}
	ranked := Rank(snapshot, "", Options{Intent: "fix auth"})
	exact := componentFor(ranked, "internal/auth.go", ComponentIntent)
	prefix := componentFor(ranked, "author.go", ComponentIntent)
	if exact <= prefix || prefix <= 0 {
		t.Fatalf("intent components: exact=%d prefix=%d (exact must win, prefix half-strength)", exact, prefix)
	}
	if componentFor(ranked, "loiter.go", ComponentIntent) != 0 {
		t.Fatalf("mid-word substring 'oiter' must not boost: %+v", ranked)
	}
	// 'tho' inside author is a mid-word substring of the token, not a prefix
	if matched, _ := MatchPart(SplitIdentifier("author"), "tho"); matched {
		t.Fatal("MatchPart must reject mid-word substrings")
	}
}

func TestIntentMatchingMatchesCompoundIdentifiersAndPrefixVariants(t *testing.T) {
	t.Parallel()
	snapshot := analyze.Snapshot{
		Files: []analyze.File{
			{Path: "service.go", Language: "go"},
			{Path: "other.go", Language: "go"},
		},
		Symbols: []analyze.Symbol{{Name: "ParseConfig", Location: analyze.Location{Path: "service.go", Line: 1}}},
	}
	ranked := Rank(snapshot, "", Options{Intent: "parse config"})
	if scoreOf(ranked, "service.go") <= scoreOf(ranked, "other.go") {
		t.Fatalf("compound ParseConfig should match 'parse config': %+v", ranked)
	}

	prefixRanked := Rank(analyze.Snapshot{
		Files:   []analyze.File{{Path: "deps.go", Language: "go"}},
		Symbols: []analyze.Symbol{{Name: "LoadDependencyGraph", Location: analyze.Location{Path: "deps.go", Line: 1}}},
	}, "", Options{Intent: "dependency graphs"})
	if scoreOf(prefixRanked, "deps.go") <= 0 {
		t.Fatal("prefix variant LoadDependencyGraph should partially match 'dependency'")
	}
}

func TestClassSignalsDemoteNonProductionRoles(t *testing.T) {
	t.Parallel()
	snapshot := analyze.Snapshot{Files: []analyze.File{
		{Path: "service.go", Language: "go"},
		{Path: "examples/service.go", Language: "go"},
		{Path: "internal/legacy/service.go", Language: "go"},
		{Path: "docs/service.md", Language: "markdown"},
		{Path: "generated/service.go", Language: "go"},
	}}
	ranked := Rank(snapshot, "", Options{})
	service := scoreOf(ranked, "service.go")
	for _, path := range []string{"examples/service.go", "internal/legacy/service.go", "docs/service.md", "generated/service.go"} {
		if componentFor(ranked, path, ComponentClassDemote) >= 0 {
			t.Fatalf("%s missing class demotion: %+v", path, ranked)
		}
		if scoreOf(ranked, path) >= service {
			t.Fatalf("%s outranks production service.go: %+v", path, ranked)
		}
	}
	// includeTests must not restore demoted non-test classes
	included := Rank(snapshot, "", Options{IncludeTests: true})
	if componentFor(included, "examples/service.go", ComponentClassDemote) == 0 {
		t.Fatal("includeTests must not clear example demotion")
	}
}

func TestClassSignalsTestDemotionOnlyForNonGoTestFiles(t *testing.T) {
	t.Parallel()
	snapshot := analyze.Snapshot{Files: []analyze.File{
		{Path: "a_test.go", Language: "go"},
		{Path: "test/helper.py", Language: "python"},
	}}
	defaultRank := Rank(snapshot, "", Options{})
	// Go suffix already carries -15 via ComponentTestDemote; the class signal
	// adds -10 only for files the suffix check cannot see.
	if componentFor(defaultRank, "a_test.go", ComponentClassDemote) != 0 {
		t.Fatalf("go test file double-demoted: %+v", defaultRank)
	}
	if componentFor(defaultRank, "test/helper.py", ComponentClassDemote) != classDemoteTest {
		t.Fatalf("non-go test-classed file not demoted: %+v", defaultRank)
	}
	included := Rank(snapshot, "", Options{IncludeTests: true})
	if componentFor(included, "test/helper.py", ComponentClassDemote) != 0 {
		t.Fatal("includeTests must clear non-go test-class demotion")
	}
}

func scoreOf(ranked []RankedFile, path string) int {
	for _, rf := range ranked {
		if rf.Path == path {
			return rf.Score
		}
	}
	return 0
}
