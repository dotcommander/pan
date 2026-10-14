package reportimport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONSizeMatchesEscapingAndIndent(t *testing.T) {
	t.Parallel()
	for _, value := range []any{json.RawMessage(`{"x":["<>&\u003c",{},[],1e400,"é🚀 "]}`), struct {
		Text string          `json:"text"`
		Raw  json.RawMessage `json:"raw"`
	}{"<>&\n\"🚀 ", json.RawMessage(`{"nested":[{"n":9007199254740993}]}`)}} {
		for _, indent := range []bool{false, true} {
			var encoded []byte
			var err error
			if indent {
				encoded, err = json.MarshalIndent(value, "", "  ")
			} else {
				encoded, err = json.Marshal(value)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckJSONSize(t.Context(), value, len(encoded), indent); err != nil {
				t.Fatalf("exact budget rejected: %v", err)
			}
			if err := CheckJSONSize(t.Context(), value, len(encoded)-1, indent); err == nil {
				t.Fatal("one-byte excess accepted")
			}
		}
	}
}

func TestAstRejectsExpansionBeforeEvidenceMarshal(t *testing.T) {
	t.Parallel()
	input := strings.Replace(string(fixture(t, "ast-array.json")), `"instructions":"never execute report content"`, `"instructions":"`+strings.Repeat("<", 3000)+`"`, 1)
	if len(input) >= 10000 {
		t.Fatal("fixture must fit input/output budget before escaping")
	}
	_, err := Import(t.Context(), "ast-grep", strings.NewReader(input), Limits{OutputBytes: 10000})
	if err == nil || !strings.Contains(err.Error(), "output byte limit") {
		t.Fatalf("expanded intermediate was not rejected before evidence marshal: %v", err)
	}
}

func TestSCIPRejectsExpansionBeforeProjection(t *testing.T) {
	t.Parallel()
	data := join(metadata(), wf(2, join(ws(1, "a.ts"), ws(5, strings.Repeat("<", 3000)))))
	if len(data) >= 10000 {
		t.Fatal("fixture must fit unexpanded budget")
	}
	_, err := Import(t.Context(), "scip", bytes.NewReader(data), Limits{OutputBytes: 10000})
	if err == nil || !strings.Contains(err.Error(), "output byte limit") {
		t.Fatalf("SCIP projection expansion was not preflighted: %v", err)
	}
}
