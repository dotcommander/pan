package reportimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAstEvidenceAndFraming(t *testing.T) {
	t.Parallel()
	data := fixture(t, "ast-array.json")
	r, err := Import(t.Context(), "ast-grep", bytes.NewReader(data), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Schema != "tool-report/v1" || !r.Import.Complete || r.Import.Scope != "captured input only" || r.Source.Coverage != "unknown" || r.Source.Freshness != "unknown" || r.Origin.Version != nil {
		t.Fatalf("incorrect provenance: %+v", r)
	}
	digest := sha256.Sum256(data)
	if r.Origin.InputSHA256 != hex.EncodeToString(digest[:]) || r.Origin.InputBytes != len(data) {
		t.Fatal("wrong input identity")
	}
	if len(r.Views) != 1 || r.Views[0].Path != "/missing/project/é.ts" || !slices.Equal(r.Views[0].Range, []int64{0, 2, 0, 10}) {
		t.Fatalf("wrong coordinates: %+v", r.Views)
	}
	for _, lexeme := range []string{"9007199254740993", "1e400", `"negativeZero":-0`, `"metaVariables"`, `"replacementOffsets"`, `"labels"`} {
		if !bytes.Contains(r.Evidence, []byte(lexeme)) {
			t.Fatalf("lost %s", lexeme)
		}
	}
	var records []json.RawMessage
	if err := json.Unmarshal(r.Evidence, &records); err != nil {
		t.Fatal(err)
	}
	stream := append(append([]byte{}, records[0]...), '\n')
	stream = append(stream, records[0]...)
	s, err := Import(t.Context(), "ast-grep", bytes.NewReader(stream), Limits{})
	if err != nil || s.Import.Records != 2 || s.Views[1].Ordinal != 1 {
		t.Fatalf("stream lost matches: %v %+v", err, s)
	}
	for _, empty := range []string{"[]", "", "\n \n"} {
		r, err := Import(t.Context(), "ast-grep", strings.NewReader(empty), Limits{})
		if err != nil || r.Import.Records != 0 || string(r.Evidence) != "[]" {
			t.Fatalf("empty match input: %v %+v", err, r)
		}
	}
}

func TestCloneEvidence(t *testing.T) {
	t.Parallel()
	data := fixture(t, "jscpd-v5.json")
	r, err := Import(t.Context(), "jscpd", bytes.NewReader(data), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Views) != 1 || r.Views[0].Path != `C:\missing\é.ts` || r.Views[0].OtherPath != "/missing/b.ts" || !slices.Equal(r.Views[0].Range, []int64{2, 3, 4, 8}) {
		t.Fatalf("pair identity changed: %+v", r.Views)
	}
	for _, text := range []string{`"similarity":0.91`, `"nodes":9`, `"history"`, `"blame"`, `9007199254740993`} {
		if !bytes.Contains(r.Evidence, []byte(text)) {
			t.Fatalf("lost %s", text)
		}
	}
	if !strings.Contains(r.Coordinates, "unverified") || !strings.Contains(strings.Join(r.Limitations, " "), "approximate") {
		t.Fatal("invented coordinate or clone certainty")
	}
}

func TestReportDeterminism(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ format, file string }{{"ast-grep", "ast-array.json"}, {"jscpd", "jscpd-v5.json"}} {
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()
			data := fixture(t, tc.file)
			a, err := Import(t.Context(), tc.format, bytes.NewReader(data), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			b, err := Import(t.Context(), tc.format, bytes.NewReader(data), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			if !bytes.Equal(x, y) {
				t.Fatal("same input produced different output")
			}
		})
	}
}

func TestReportCancellationAndReadFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Import(ctx, "ast-grep", strings.NewReader("[]"), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	want := errors.New("reader failed")
	if _, err := Import(t.Context(), "ast-grep", failingReader{want}, Limits{}); !errors.Is(err, want) {
		t.Fatalf("reader failure: %v", err)
	}
	if _, err := Import(t.Context(), "unknown", strings.NewReader("[]"), Limits{}); err == nil {
		t.Fatal("unknown format accepted")
	}
	if _, err := Import(t.Context(), "ast-grep", nil, Limits{}); err == nil {
		t.Fatal("missing input accepted")
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }
