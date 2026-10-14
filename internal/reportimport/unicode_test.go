package reportimport

import (
	"bytes"
	"strings"
	"testing"
)

func TestRejectUnpairedSurrogatesBeforeIdentityDecode(t *testing.T) {
	t.Parallel()
	data := string(fixture(t, "ast-array.json"))
	for _, replacement := range []string{`x\ud800`, `x\udfff`, `x\uD800x`, `x\ud800\u0041`} {
		t.Run(replacement, func(t *testing.T) {
			t.Parallel()
			input := strings.ReplaceAll(data, "/missing/project/é.ts", replacement)
			if _, err := Import(t.Context(), "ast-grep", strings.NewReader(input), Limits{}); err == nil || !strings.Contains(err.Error(), "surrogate") {
				t.Fatalf("unpaired identity accepted: %v", err)
			}
		})
	}
	for _, input := range []string{`[{"\ud800":1}]`, `{"statistics":{},"duplicates":[],"extra":"\udc00"}`} {
		format := "ast-grep"
		if strings.HasPrefix(input, "{") {
			format = "jscpd"
		}
		if _, err := Import(t.Context(), format, strings.NewReader(input), Limits{}); err == nil || !strings.Contains(err.Error(), "surrogate") {
			t.Fatalf("unpaired key/value accepted: %v", err)
		}
	}
}

func TestValidUnicodeIdentityAndRawEvidence(t *testing.T) {
	t.Parallel()
	data := string(fixture(t, "ast-array.json"))
	for _, tc := range []struct{ raw, path string }{{`x\ud83d\ude80`, "x🚀"}, {`x\uFFFD`, "x�"}, {"x�", "x�"}, {`x\\ud800`, `x\ud800`}} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			input := strings.ReplaceAll(data, "/missing/project/é.ts", tc.raw)
			r, err := Import(t.Context(), "ast-grep", strings.NewReader(input), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if r.Views[0].Path != tc.path {
				t.Fatalf("identity changed: %q", r.Views[0].Path)
			}
			for _, lexeme := range []string{tc.raw, "9007199254740993", "1e400"} {
				if !bytes.Contains(r.Evidence, []byte(lexeme)) {
					t.Fatalf("raw evidence lost %q", lexeme)
				}
			}
		})
	}
}
