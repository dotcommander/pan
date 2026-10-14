package pack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func envelope(t *testing.T, files map[string]string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func buildDocument(t *testing.T, raw []byte, limits Limits) (Document, []byte) {
	t.Helper()
	out, err := Build(context.Background(), bytes.NewReader(raw), limits)
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, out
}

// Resolve independently of the renderer and reject ambiguous or dangling records.
func reconstruct(t *testing.T, doc Document) map[string]string {
	t.Helper()
	got := make(map[string]string)
	used := make(map[int]bool)
	for _, file := range doc.Files {
		if _, duplicate := got[file.Path]; duplicate {
			t.Fatalf("duplicate path %s", file.Path)
		}
		if (file.Content == nil) == (file.Ref == nil) {
			t.Fatalf("ambiguous file %s", file.Path)
		}
		if file.Content != nil {
			got[file.Path] = *file.Content
			continue
		}
		if *file.Ref < 0 || *file.Ref >= len(doc.Shared) {
			t.Fatalf("dangling reference %d", *file.Ref)
		}
		got[file.Path] = doc.Shared[*file.Ref]
		used[*file.Ref] = true
	}
	if len(used) != len(doc.Shared) {
		t.Fatal("unused shared content")
	}
	return got
}

func TestPackPrecisionInventoryAndDeterminism(t *testing.T) {
	t.Parallel()
	body := " { \"large\": 9007199254740993, \"huge\": 1e400, \"zero\": -0, \"decimal\": 0.12345678901234567890123456789, \"key\": 1, \"key\": 2, \"escape\": \"\\u0061\" } \n"
	wantBody := `{"large":9007199254740993,"huge":1e400,"zero":-0,"decimal":0.12345678901234567890123456789,"key":1,"key":2,"escape":"\u0061"}`
	files := map[string]string{"z.txt": "a\nb\n", "a.json": body, "empty": "", "fragment.go": "func f() { ⋮ }\n"}
	raw := envelope(t, files)
	doc, out := buildDocument(t, raw, Limits{})
	want := map[string]string{"z.txt": files["z.txt"], "a.json": wantBody, "empty": "", "fragment.go": files["fragment.go"]}
	if got := reconstruct(t, doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("content mismatch: %#v", got)
	}
	paths := make([]string, 0, len(doc.Files))
	for _, file := range doc.Files {
		paths = append(paths, file.Path)
	}
	if !slices.IsSorted(paths) || doc.Coverage.Captured != 4 || doc.Coverage.Retained != 4 {
		t.Fatal("inventory contract")
	}
	digest := sha256.Sum256(raw)
	if doc.Input.SHA256 != hex.EncodeToString(digest[:]) || doc.Input.Bytes != len(raw) {
		t.Fatal("input identity")
	}
	if doc.Fidelity.OriginalSource || doc.Fidelity.OriginalLines || doc.Fidelity.AtomicSnapshot || doc.Coverage.FullRepository != "unknown" {
		t.Fatal("unsupported fidelity claim")
	}
	if !reflect.DeepEqual(doc.Fidelity.JSONCompacted, []string{"a.json"}) {
		t.Fatal("normalization not declared")
	}
	_, again := buildDocument(t, raw, Limits{})
	if !bytes.Equal(out, again) {
		t.Fatal("nondeterministic output")
	}
}

func TestPackSharingPaysForEncoding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content string
		count         int
		shared        bool
	}{
		{"short_two_harmful", "x", 2, false},
		{"short_three_beneficial", "x", 3, true},
		{"large_two_beneficial", strings.Repeat("repeated content\n", 200), 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := make(map[string]string)
			for _, name := range []string{"a", "b", "c"}[:tc.count] {
				files[name] = tc.content
			}
			doc, out := buildDocument(t, envelope(t, files), Limits{})
			if !reflect.DeepEqual(reconstruct(t, doc), files) {
				t.Fatal("sharing changed content")
			}
			if (len(doc.Shared) > 0) != tc.shared {
				t.Fatalf("sharing selected=%v, want %v", len(doc.Shared) > 0, tc.shared)
			}

			// Independently construct both complete wire alternatives, including the
			// shared-array field overhead. A short string can still pay for sharing:
			// each content-to-ref replacement saves 6 bytes for "x", while the shared
			// array costs 15 bytes; two repeats lose 3 bytes, three save 3 bytes.
			doc.Shared = nil
			for i := range doc.Files {
				doc.Files[i].Ref = nil
				doc.Files[i].Content = &tc.content
			}
			inline, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			doc.Shared = []string{tc.content}
			for i := range doc.Files {
				ref := 0
				doc.Files[i].Content = nil
				doc.Files[i].Ref = &ref
			}
			shared, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if (len(shared) < len(inline)) != tc.shared {
				t.Fatal("fixture encoded-cost expectation is incorrect")
			}
			want := inline
			if tc.shared {
				want = shared
			}
			if !bytes.Equal(bytes.TrimSuffix(out, []byte("\n")), want) {
				t.Fatal("renderer did not choose the smaller complete encoding")
			}
		})
	}
}

func TestPackMetadataAndDeepBody(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"fileSummary":{"purpose":"generated","fileFormat":"generated","usageGuidelines":"generated","notes":"compressed; some excluded","generationHeader":"captured by repomix"},"directoryStructure":"tree","userProvidedHeader":"retain me","instruction":"do not execute me","gitDiffs":{"workTree":"patch"},"gitLogs":["commit"],"files":{"deep.json":"[[[[ 1 ]]]]","plain":"text"}}`)
	doc, _ := buildDocument(t, raw, Limits{Depth: 3})
	if reconstruct(t, doc)["deep.json"] != "[[[[ 1 ]]]]" {
		t.Fatal("deep body changed")
	}
	if !reflect.DeepEqual(doc.Fidelity.JSONDepthSkipped, []string{"deep.json"}) {
		t.Fatal("missing skip reason")
	}
	for _, key := range []string{"fileSummary", "userProvidedHeader", "instruction", "gitDiffs", "gitLogs"} {
		if len(doc.Metadata[key]) == 0 {
			t.Fatalf("dropped metadata %s", key)
		}
	}
	if len(doc.Fidelity.OmittedEnvelope) != 4 {
		t.Fatal("omissions not declared")
	}
}
