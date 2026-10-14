package reportimport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestSCIPPreservesSemanticEvidence(t *testing.T) {
	t.Parallel()
	symbol := join(ws(1, "local 0"), ws(3, "symbol documentation"), wf(4, join(ws(1, "external symbol"), wi(2, 1), wi(5, 1))), wi(5, 999), ws(6, "display"), wf(7, join(ws(4, "typescript"), ws(5, "signature"))), ws(8, "enclosing"))
	doc := wf(2, join(ws(1, "a.ts"), wf(2, occurrence()), wf(3, symbol), ws(4, "typescript"), wi(6, 2)))
	data := join(metadata(), doc, wf(3, join(ws(1, "external symbol"), ws(3, "external docs"))), wi(100, 123))
	r, err := Import(t.Context(), "scip", bytes.NewReader(data), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Import.Documents != 1 || len(r.Views) != 1 || r.Views[0].LocalNamespace != "a.ts" || r.Views[0].Roles != 137 || !slices.Equal(r.Views[0].Range, []int64{0, 1, 0, 3}) {
		t.Fatalf("lost semantic identity: %+v", r)
	}
	if r.Origin.Producer == nil || *r.Origin.Producer != "fixture-indexer" || r.Origin.Version == nil || *r.Origin.Version != "1.2.3" {
		t.Fatal("lost producer")
	}
	recovered, err := base64.StdEncoding.DecodeString(r.OriginalBase64)
	if err != nil || !bytes.Equal(recovered, data) {
		t.Fatal("binary cannot be recovered exactly")
	}
	for _, s := range []string{"symbol documentation", "relationships", "signature_documentation", "external docs", "diagnostic", "enclosing"} {
		if !bytes.Contains(r.Evidence, []byte(s)) {
			t.Fatalf("lost known field %s", s)
		}
	}
	unsupported := strings.Join(r.Unsupported, " ")
	for _, s := range []string{"unknown wire", "unknown enum", "uninterpreted bits"} {
		if !strings.Contains(unsupported, s) {
			t.Fatalf("missing unknown declaration %s: %s", s, unsupported)
		}
	}
	again, err := Import(t.Context(), "scip", bytes.NewReader(data), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(r)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Fatal("nondeterministic SCIP projection")
	}
}

func TestSCIPCoordinateEncodings(t *testing.T) {
	t.Parallel()
	for _, enc := range []uint64{0, 1, 2, 3, 99} {
		t.Run(string(rune('a'+enc)), func(t *testing.T) {
			t.Parallel()
			r, err := Import(t.Context(), "scip", bytes.NewReader(join(metadata(), document("a.ts", join(packed(1, 2, 3, 4, 5), ws(2, "s")), enc))), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(r.Views[0].Range, []int64{2, 3, 4, 5}) || r.Views[0].PositionEncoding == "" {
				t.Fatal("coordinate conversion or encoding loss")
			}
		})
	}
}
