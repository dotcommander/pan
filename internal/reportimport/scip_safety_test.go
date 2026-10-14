package reportimport

import (
	"bytes"
	"strings"
	"testing"
)

func TestSCIPRejectsAmbiguityAndUnsafeInput(t *testing.T) {
	t.Parallel()
	base := join(packed(1, 0, 1, 3), ws(2, "s"))
	cases := map[string][]byte{
		"noMetadata": document("a", base, 1), "metadataTwice": join(metadata(), metadata()), "duplicateSingular": join(metadata(), wf(2, join(ws(1, "a"), ws(1, "b")))),
		"oneof": join(metadata(), document("a", join(base, wf(8, wi(3, 3)), wf(9, wi(3, 1))), 1)), "conflictingRange": join(metadata(), document("a", join(base, wf(8, join(wi(2, 2), wi(3, 3)))), 1)),
		"missingRange": join(metadata(), document("a", ws(2, "s"), 1)), "longRange": join(metadata(), document("a", packed(1, 0, 1, 2, 3, 4), 1)), "traversal": join(metadata(), document("../a", base, 1)), "absolute": join(metadata(), document("/a", base, 1)), "duplicatePath": join(metadata(), document("a", base, 1), document("a", base, 1)), "invalidUTF8": join(metadata(), wf(2, wf(1, []byte{255}))), "badWire": join(metadata(), []byte{0x12, 0xff}), "wrongType": join(metadata(), wi(2, 1)),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Import(t.Context(), "scip", bytes.NewReader(data), Limits{}); err == nil {
				t.Fatal("unsafe SCIP accepted")
			}
		})
	}
}

func TestSCIPBoundsBeforeUnmarshal(t *testing.T) {
	t.Parallel()
	data := join(metadata(), document("a.ts", occurrence(), 1), document("b.ts", occurrence(), 1))
	for name, l := range map[string]Limits{"documents": {Documents: 1}, "depth": {Depth: 1}, "string": {StringBytes: 3}, "path": {PathBytes: 2}, "records": {Records: 2}, "input": {InputBytes: 2}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Import(t.Context(), "scip", bytes.NewReader(data), l); err == nil {
				t.Fatal("SCIP limit ignored")
			}
		})
	}
	// Metadata uses the complete budget; malformed next field must not be decoded.
	_, err := Import(t.Context(), "scip", bytes.NewReader(join([]byte{0x0a, 0}, []byte{0x12, 0xff})), Limits{Records: 1})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("count was not enforced before next body: %v", err)
	}
}
