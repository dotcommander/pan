package ranking

import (
	"slices"
	"testing"
)

func TestSplitIdentifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		token string
		want  []string
	}{
		{"getHTTPResponse", []string{"gethttpresponse", "get", "http", "response"}},
		{"HTMLParser", []string{"htmlparser", "html", "parser"}},
		{"OAuth2", []string{"oauth2", "o", "auth2"}},
		{"parse_config", []string{"parse_config", "parse", "config"}},
		{"cache", []string{"cache"}},
		{"GetUserById", []string{"getuserbyid", "get", "user", "by", "id"}},
	}
	for _, tt := range tests {
		if got := SplitIdentifier(tt.token); !slices.Equal(got, tt.want) {
			t.Errorf("SplitIdentifier(%q) = %v, want %v", tt.token, got, tt.want)
		}
	}
}

func TestMatchPartSeparatesExactPrefixAndSubstring(t *testing.T) {
	t.Parallel()
	parts := []string{"authorizer", "author", "auth"}

	// auth is a real sub-token: exact match
	if matched, exact := MatchPart(parts, "auth"); !matched || !exact {
		t.Errorf("auth: matched=%v exact=%v, want true/true", matched, exact)
	}
	// author matches as its own sub-token even though it prefixes authorizer
	if matched, exact := MatchPart([]string{"authorizer"}, "author"); !matched || exact {
		t.Errorf("author vs [authorizer]: matched=%v exact=%v, want true/false (prefix)", matched, exact)
	}
	// morphological variant that is not a prefix never matches at all
	if matched, exact := MatchPart([]string{"dependencies"}, "dependency"); matched || exact {
		t.Errorf("dependency vs [dependencies]: matched=%v, want false (not a prefix)", matched)
	}
	// singular/plural prefix variant matches at partial strength
	if matched, exact := MatchPart([]string{"configs"}, "config"); !matched || exact {
		t.Errorf("config vs [configs]: matched=%v exact=%v, want true/false", matched, exact)
	}
	// incidental substring never matches
	if matched, _ := MatchPart([]string{"author"}, "tho"); matched {
		t.Error("tho vs [author]: matched, want false")
	}
	// snake normalization
	if matched, exact := MatchPart([]string{"parse_config"}, "parseconfig"); !matched || !exact {
		t.Errorf("parseconfig vs [parse_config]: matched=%v exact=%v, want true/true", matched, exact)
	}
	// prefix floor: short prefixes do not match
	if matched, _ := MatchPart([]string{"configuration"}, "co"); matched {
		t.Error("co vs [configuration]: matched, want false (under prefix floor)")
	}
	if matched, exact := MatchPart([]string{"configuration"}, "config"); !matched || exact {
		t.Errorf("config vs [configuration]: matched=%v exact=%v, want true/false", matched, exact)
	}
}

func TestPathTermsSplitsSegmentsWithoutExtensions(t *testing.T) {
	t.Parallel()
	got := PathTerms("internal/CLI/context.go")
	want := []string{"internal", "cli", "context"}
	if !slices.Equal(got, want) {
		t.Fatalf("PathTerms = %v, want %v", got, want)
	}
	if got := PathTerms("./pkg/getHTTPResponse.ts"); !slices.Contains(got, "http") || !slices.Contains(got, "response") {
		t.Fatalf("PathTerms(getHTTPResponse.ts) = %v, want camel sub-tokens", got)
	}
}

func TestExpandTermsExpandsCompoundsAndDropsStopwords(t *testing.T) {
	t.Parallel()
	got := ExpandTerms("fix the ParseConfig cache", 3, CommonStopwords)
	want := []string{"parseconfig", "parse", "config", "cache"}
	if !slices.Equal(got, want) {
		t.Fatalf("ExpandTerms = %v, want %v", got, want)
	}
	if got := ExpandTerms("parse config", 2, CommonStopwords); !slices.Equal(got, []string{"parse", "config"}) {
		t.Fatalf("ExpandTerms(min2) = %v", got)
	}
}
