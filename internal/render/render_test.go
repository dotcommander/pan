package render_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/render"
)

func sampleInputs() (analyze.Status, []analyze.Diagnostic) {
	status := analyze.Status{
		Complete:     false,
		Limits:       []string{"max_nodes", "max_files"},
		Skipped:      []string{"b (excluded)", "a (oversized)"},
		SkippedCount: 2,
	}
	diagnostics := []analyze.Diagnostic{
		{Level: "warning", Message: "second", Location: &analyze.Location{Path: "z.go", Line: 2}},
		{Level: "warning", Message: "first", Location: &analyze.Location{Path: "a.go", Line: 1}},
		{Level: "info", Message: "note"},
	}
	return status, diagnostics
}

func TestJSONRenderIsCanonicalAndImmutable(t *testing.T) {
	t.Parallel()
	status, diagnostics := sampleInputs()
	envelope := render.NewEnvelope([]string{"scan", "overview"}, "/repo", status, map[string]int{"files": 2}, diagnostics)

	// Mutate the caller's slices after construction.
	status.Limits[0] = "zzz"
	status.Skipped[0] = "zzz"
	diagnostics[0] = analyze.Diagnostic{Level: "error", Message: "mutated"}

	var first, second bytes.Buffer
	if err := render.Write(&first, "json", envelope); err != nil {
		t.Fatal(err)
	}
	if err := render.Write(&second, "json", envelope); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("nondeterministic JSON output:\n%s\n---\n%s", first.String(), second.String())
	}
	out := first.String()
	if !strings.Contains(out, `"schema_version": "pan/v1"`) {
		t.Fatalf("missing stamped schema version:\n%s", out)
	}
	assertOrdered(t, out, `"max_files"`, `"max_nodes"`, "limits not sorted")
	assertOrdered(t, out, "a (oversized)", "b (excluded)", "skipped not sorted")
	assertOrdered(t, out, `"path": "a.go"`, `"path": "z.go"`, "diagnostics not sorted")
	if strings.Contains(out, "mutated") || strings.Contains(out, "zzz") {
		t.Fatalf("rendered caller mutations:\n%s", out)
	}
}

func TestTextRenderIsExact(t *testing.T) {
	t.Parallel()
	status, diagnostics := sampleInputs()
	envelope := render.NewEnvelope([]string{"scan", "overview"}, "/repo", status, map[string]int{"files": 2}, diagnostics)

	var out bytes.Buffer
	if err := render.Write(&out, "text", envelope); err != nil {
		t.Fatal(err)
	}
	want := "pan pan/v1\n" +
		"command: scan overview\n" +
		"repository: /repo\n" +
		"complete: false\n" +
		"limits: max_files, max_nodes\n" +
		"skipped: 2\n" +
		"  - a (oversized)\n" +
		"  - b (excluded)\n" +
		"diagnostics: 3\n" +
		"\n" +
		"{\n  \"files\": 2\n}\n"
	if out.String() != want {
		t.Fatalf("text output mismatch:\n got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestTextRenderBoundsSkippedSection(t *testing.T) {
	t.Parallel()
	skipped := make([]string, 25)
	for i := range skipped {
		skipped[i] = strings.Repeat("f", 2) + strings.Repeat(string(rune('a'+i%26)), 5) + ".go (oversized)"
	}
	status := analyze.Status{Complete: false, Skipped: skipped, SkippedCount: len(skipped), Limits: []string{"max_file_bytes"}}
	envelope := render.NewEnvelope([]string{"scan", "files"}, "/repo", status, nil, nil)

	var out bytes.Buffer
	if err := render.Write(&out, "text", envelope); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if got := strings.Count(text, "  - "); got != 11 {
		t.Fatalf("rendered %d skipped lines, want 10 entries plus sentinel", got)
	}
	if !strings.Contains(text, "... (15 more)") {
		t.Fatalf("missing truncation sentinel:\n%s", text)
	}
	if !strings.Contains(text, "skipped: 25\n") {
		t.Fatalf("missing skipped count:\n%s", text)
	}
}

func TestWriteStampsMissingVersionAndKeepsExplicit(t *testing.T) {
	t.Parallel()
	t.Run("missing version is stamped", func(t *testing.T) {
		t.Parallel()
		e := render.Envelope{Command: []string{"brief"}, Repository: "/r", Analysis: analyze.Status{Complete: true}, Result: nil}
		var out bytes.Buffer
		if err := render.Write(&out, "json", e); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"schema_version": "pan/v1"`) {
			t.Fatalf("version not stamped:\n%s", out.String())
		}
	})
	t.Run("explicit version is preserved", func(t *testing.T) {
		t.Parallel()
		e := render.Envelope{SchemaVersion: "pan/v2", Command: []string{"brief"}, Repository: "/r", Result: nil}
		var out bytes.Buffer
		if err := render.Write(&out, "json", e); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"schema_version": "pan/v2"`) {
			t.Fatalf("explicit version not preserved:\n%s", out.String())
		}
	})
}

func TestTextRenderOmitsEmptySections(t *testing.T) {
	t.Parallel()
	envelope := render.NewEnvelope([]string{"brief"}, "/repo", analyze.Status{Complete: true}, map[string]int{"files": 0}, nil)
	var out bytes.Buffer
	if err := render.Write(&out, "text", envelope); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "limits:") || strings.Contains(text, "skipped:") || strings.Contains(text, "diagnostics:") {
		t.Fatalf("empty sections rendered:\n%s", text)
	}
	if !strings.Contains(text, "complete: true") {
		t.Fatalf("missing complete line:\n%s", text)
	}
}

func assertOrdered(t *testing.T, haystack, first, second, message string) {
	t.Helper()
	i := strings.Index(haystack, first)
	j := strings.Index(haystack, second)
	if i < 0 || j < 0 || i > j {
		t.Fatalf("%s: %q at %d, %q at %d\n%s", message, first, i, second, j, haystack)
	}
}
