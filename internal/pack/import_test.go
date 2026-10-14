package pack

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPackRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	cases := []string{
		``, `[]`, `{"files":`, `{"files":{}} {}`, `{"files":null}`, `{"files":{"a":null}}`,
		`{"files":{},"files":{}}`, `{"files":{"a":"x","\u0061":"y"}}`,
		`{"files":{},"unknown":"extension"}`, `{"files":{},"fileSummary":{"notes":1,"notes":2}}`,
		`{"files":{"a":"\ud800"}}`, `{"files":{"a":"\udc00"}}`,
		string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}),
	}
	for i, input := range cases {
		t.Run(strings.Repeat("x", i+1), func(t *testing.T) {
			t.Parallel()
			if out, err := Build(context.Background(), strings.NewReader(input), Limits{}); err == nil || len(out) != 0 {
				t.Fatalf("accepted invalid input %q", input)
			}
		})
	}
}

func TestPackRejectsUnsafePaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", ".", "..", "../a", "a/../b", "a/./b", "a//b", "/a", "a/", `a\b`, "C:relative", `\\server\share`, "a\x00b", "a\nb"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			if _, err := Build(context.Background(), bytes.NewReader(envelope(t, map[string]string{path: "x"})), Limits{}); err == nil {
				t.Fatalf("accepted unsafe path %q", path)
			}
		})
	}
}

func TestPackLimits(t *testing.T) {
	t.Parallel()
	raw := envelope(t, map[string]string{"long/path": "abcd", "other": "efgh"})
	cases := map[string]Limits{
		"input": {InputBytes: int64(len(raw) - 1)}, "decoded": {DecodedBytes: 7}, "file": {FileBytes: 3},
		"count": {Files: 1}, "path": {PathBytes: 3}, "depth": {Depth: 1}, "output": {OutputBytes: 10},
		"negative": {Files: -1}, "raised_depth": {Depth: 129},
	}
	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Build(context.Background(), bytes.NewReader(raw), limits); err == nil {
				t.Fatal("limit not enforced")
			}
		})
	}
	_, _ = buildDocument(t, raw, Limits{InputBytes: int64(len(raw)), DecodedBytes: 8, FileBytes: 4, Files: 2, PathBytes: 9})
}

func TestPackCanceledAndMissingStream(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, strings.NewReader(`{"files":{}}`), Limits{}); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := Build(context.Background(), nil, Limits{}); err == nil {
		t.Fatal("missing stream accepted")
	}
}

func TestPackUnicodeEscapes(t *testing.T) {
	t.Parallel()
	doc, _ := buildDocument(t, []byte(`{"files":{"emoji":"\ud83d\ude00","literal":"\\ud800"}}`), Limits{})
	got := reconstruct(t, doc)
	if got["emoji"] != "😀" || got["literal"] != `\ud800` {
		t.Fatal("Unicode escape decoding changed")
	}
}

func TestPackJSONFileCountRejectsBeforeExcessBody(t *testing.T) {
	t.Parallel()
	// The second key crosses the limit. Its malformed value must never be
	// parsed: the count error proves early rejection independently of timing
	// or allocator behavior. The pre-fix validator instead reports syntax.
	raw := `{"files":{"accepted":"body","excess":!invalid}}`
	out, err := Build(context.Background(), strings.NewReader(raw), Limits{Files: 1})
	if err == nil || !strings.Contains(err.Error(), "file count limit 1") {
		t.Fatalf("expected count rejection before excess body, got %v", err)
	}
	if len(out) != 0 {
		t.Fatal("rejected inventory produced output")
	}
}
