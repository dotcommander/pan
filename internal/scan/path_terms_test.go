package scan

import (
	"strings"
	"testing"
)

func TestPreparedPathTermMatchingParity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, path, term string
		want             bool
	}{
		{name: "directory boundary", path: "internal/auth/model.go", term: "auth", want: true},
		{name: "prefix in word", path: "internal/author/model.go", term: "auth"},
		{name: "suffix in word", path: "internal/preauth/model.go", term: "auth"},
		{name: "underscore boundary", path: "auth_token.go", term: "auth", want: true},
		{name: "digit is word", path: "auth2.go", term: "auth"},
		{name: "ascii letter is word", path: "zauth.go", term: "auth"},
		{name: "non-ascii boundary", path: "éauthé.go", term: "auth", want: true},
		{name: "punctuation substring", path: "myapi/v2endpoint", term: "api/v2", want: true},
		{name: "punctuation not present", path: "api_v2/endpoint", term: "api/v2"},
		{name: "dot substring", path: "my.authenticator", term: ".auth", want: true},
		{name: "hyphen substring", path: "pre-authored", term: "-auth", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matcher := preparePathTerm(tc.term)
			if got := matcher.matches(tc.path); got != tc.want {
				t.Fatalf("prepared %q in %q = %t, want %t", tc.term, tc.path, got, tc.want)
			}
			if got := pathTermMatches(tc.path, tc.term); got != tc.want {
				t.Fatalf("one-off %q in %q = %t, want %t", tc.term, tc.path, got, tc.want)
			}
		})
	}
}

func TestRiskPathTermTableReusesPreparedMatchers(t *testing.T) {
	t.Parallel()
	terms := riskPathTermTable()
	var auth *pathTermMatcher
	for i := range terms {
		if terms[i].Term == "auth" {
			auth = &terms[i].matcher
			break
		}
	}
	if auth == nil || auth.re == nil {
		t.Fatal("risk table did not prepare the auth word matcher")
	}
	compiled := auth.re
	for _, path := range []string{"internal/auth/login.go", "pkg/auth/handler.go"} {
		var reasons []string
		addPathTermRisk(path, terms, func(_ int, _, reason string) { reasons = append(reasons, reason) })
		if !auth.matches(path) || !strings.Contains(strings.Join(reasons, ","), "path signals authentication") {
			t.Fatalf("auth term missing for %q: %v", path, reasons)
		}
		if auth.re != compiled {
			t.Fatal("risk table replaced its matcher while scoring files")
		}
	}
}

func TestCustomPathTermsReusePreparedMatchers(t *testing.T) {
	t.Parallel()
	terms := prepareCustomTerms([]PathTerm{{Term: "AUTH", Weight: 3}, {Term: "API/V2", Weight: 2}})
	if terms[0].matcher.re == nil || terms[1].matcher.re != nil {
		t.Fatal("custom word and punctuation terms must use their respective prepared matching forms")
	}
	compiled := terms[0].matcher.re
	for _, path := range []string{"pkg/auth/handler.go", "internal/auth/login.go"} {
		if !terms[0].matcher.matches(path) || terms[0].matcher.re != compiled {
			t.Fatalf("custom word matcher was not reused for %q", path)
		}
	}
	if !terms[1].matcher.matches("myapi/v2endpoint") || terms[1].Term != "API/V2" {
		t.Fatal("custom punctuation matching or original display term changed")
	}
}
