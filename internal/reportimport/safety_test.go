package reportimport

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONSafety(t *testing.T) {
	t.Parallel()
	valid := string(fixture(t, "ast-array.json"))
	cases := map[string]string{
		"duplicate": `[{"file":"a","file":"b"}]`, "trailing": valid + ` {}`, "utf8": string([]byte{0xff}), "traversal": strings.ReplaceAll(valid, "/missing/project/é.ts", "/missing/../é.ts"), "windowsTraversal": strings.ReplaceAll(valid, "/missing/project/é.ts", `C:\\..\\é.ts`), "missingIdentity": strings.ReplaceAll(valid, `"file":`, `"notFile":`), "badRange": strings.Replace(valid, `"line":0`, `"line":-1`, 1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Import(t.Context(), "ast-grep", strings.NewReader(input), Limits{}); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
	for name, l := range map[string]Limits{"input": {InputBytes: 8}, "depth": {Depth: 2}, "values": {Records: 3}, "string": {StringBytes: 4}, "path": {PathBytes: 4}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Import(t.Context(), "ast-grep", strings.NewReader(valid), l); err == nil {
				t.Fatal("limit ignored")
			}
		})
	}
}

func TestJSONRejectsBeforeExcessValue(t *testing.T) {
	t.Parallel()
	_, err := Import(t.Context(), "ast-grep", strings.NewReader(`[{},BROKEN`), Limits{Records: 2})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected early count limit, got %v", err)
	}
}

func TestCloneInvalidVariants(t *testing.T) {
	t.Parallel()
	data := fixture(t, "jscpd-v5.json")
	for _, input := range [][]byte{[]byte(`{"statistic":{},"duplicates":[]}`), bytes.Replace(data, []byte(`"duplicates"`), []byte(`"missing"`), 1), bytes.Replace(data, []byte(`"start":2`), []byte(`"start":99`), 1)} {
		if _, err := Import(t.Context(), "jscpd", bytes.NewReader(input), Limits{}); err == nil {
			t.Fatal("invalid clone report accepted")
		}
	}
}
