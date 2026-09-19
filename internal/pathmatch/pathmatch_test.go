package pathmatch

import "testing"

func TestMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"internal/**", "internal/agent/serve.go", true},
		{"internal/**", "internal", true},
		{"internal/**", "cmd/pan/main.go", false},
		{"**/*.go", "cmd/pan/main.go", true},
		{"**/*.go", "README.md", false},
		{"**", "any/deep/path.txt", true},
		{"**", "file.txt", true},
		{"a/*/c.go", "a/b/c.go", true},
		{"a/*/c.go", "a/b/d/c.go", false},
		{"a/**/c.go", "a/b/d/c.go", true},
		{"a/**/c.go", "a/c.go", true},
		{"exact.go", "exact.go", true},
		{"exact.go", "other.go", false},
		{"*.go", "dir/main.go", false},
		{"[", "anything", false},
		{"", "", true},
		{"", "x", false},
	}
	for _, tc := range cases {
		if got := Match(tc.pattern, tc.value); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}

func TestMatchAny(t *testing.T) {
	t.Parallel()
	patterns := []string{"docs/**", "internal/**/*.go"}
	if !MatchAny(patterns, "internal/agent/serve.go") {
		t.Error("expected internal Go file to match")
	}
	if MatchAny(patterns, "cmd/main.go") {
		t.Error("did not expect cmd file to match")
	}
	if MatchAny(nil, "anything") {
		t.Error("empty patterns must match nothing")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{"internal/**", "**/*.go", "a/b.go", "*"} {
		if err := Validate(pattern); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", pattern, err)
		}
	}
	for _, pattern := range []string{"", "   ", "a/[", "**/[!"} {
		if err := Validate(pattern); err == nil {
			t.Errorf("Validate(%q) = nil, want error", pattern)
		}
	}
}
