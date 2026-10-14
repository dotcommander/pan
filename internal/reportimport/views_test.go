package reportimport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestViewsSortWithoutDroppingSourceOrder(t *testing.T) {
	t.Parallel()
	var items []json.RawMessage
	if err := json.Unmarshal(fixture(t, "ast-array.json"), &items); err != nil {
		t.Fatal(err)
	}
	first := bytes.ReplaceAll(items[0], []byte("/missing/project/é.ts"), []byte("z.ts"))
	second := bytes.ReplaceAll(items[0], []byte("/missing/project/é.ts"), []byte("a.ts"))
	input := append(append(append([]byte{'['}, first...), ','), second...)
	input = append(input, ']')
	r, err := Import(t.Context(), "ast-grep", bytes.NewReader(input), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Views) != 2 || r.Views[0].Path != "a.ts" || r.Views[0].Ordinal != 1 || r.Views[1].Path != "z.ts" || r.Views[1].Ordinal != 0 {
		t.Fatalf("wrong indexes: %+v", r.Views)
	}
	var retained []json.RawMessage
	if err := json.Unmarshal(r.Evidence, &retained); err != nil {
		t.Fatal(err)
	}
	if len(retained) != 2 || !bytes.Contains(retained[0], []byte("z.ts")) || !bytes.Contains(retained[1], []byte("a.ts")) {
		t.Fatal("source order or content lost")
	}
}

func TestPathIdentityAndTraversal(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"a/é.ts", "/missing/a", `C:\missing\a`, `\\server\share\a`} {
		if err := validPath(path, false, Limits{}.defaults()); err != nil {
			t.Fatalf("inert upstream identity rejected %q: %v", path, err)
		}
	}
	for _, path := range []string{"", "../a", `a\..\b`, "a/../b", "a\x00b", "C:a"} {
		if err := validPath(path, false, Limits{}.defaults()); err == nil {
			t.Fatalf("unsafe identity accepted %q", path)
		}
	}
	for _, path := range []string{"a//b", "a/./b", "/a", `a\b`} {
		if err := validPath(path, true, Limits{}.defaults()); err == nil {
			t.Fatalf("noncanonical SCIP path accepted %q", path)
		}
	}
}

func TestAdditiveMetadataIsInert(t *testing.T) {
	t.Parallel()
	input := strings.Replace(string(fixture(t, "ast-array.json")), `"metadata":{`, `"metadata":{"range":"not a coordinate","execute":"/missing/do-not-open",`, 1)
	r, err := Import(t.Context(), "ast-grep", strings.NewReader(input), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(r.Evidence, []byte("do-not-open")) {
		t.Fatal("additive metadata lost")
	}
}
