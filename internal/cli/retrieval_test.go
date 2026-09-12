package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func httpGoRepo() string {
	return filepath.Join("..", "..", "testdata", "http-go")
}

func runJSON(t *testing.T, args []string) string {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatalf("run %v: %v\noutput:\n%s", args, err, out.String())
	}
	return out.String()
}

func TestContextMapEmitsBudgetedMap(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "map"})
	for _, want := range []string{`"mode": "enriched"`, `"used_tokens"`, `"text":`, "service.go"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("map output missing %q:\n%s", want, got)
		}
	}
}

func TestContextMapCompactMode(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "map", "--mode", "compact"})
	if !bytes.Contains([]byte(got), []byte(`"mode": "compact"`)) {
		t.Fatalf("compact mode not reported:\n%s", got)
	}
}

func TestContextMapTextFormatsRenderDirectly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		format string
		marker string
	}{
		{format: "enriched", marker: "repository map · enriched"},
		{format: "compact", marker: "repository map · compact"},
		{format: "verbose", marker: "func:"},
		{format: "detail", marker: "func Run()"},
		{format: "lines", marker: "func Run()"},
		{format: "xml", marker: "<repomap"},
	}
	for _, test := range tests {
		t.Run(test.format, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			args := []string{"--repo", basicGoRepo(), "context", "map", "--map-format", test.format}
			if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			if strings.HasPrefix(got, "pan pan/v1") || !strings.Contains(got, test.marker) {
				t.Fatalf("%s output was wrapped or lacked %q:\n%s", test.format, test.marker, got)
			}
			if test.format == "xml" {
				var document struct {
					XMLName xml.Name `xml:"repomap"`
				}
				if err := xml.Unmarshal(out.Bytes(), &document); err != nil {
					t.Fatalf("invalid XML map: %v\n%s", err, got)
				}
			}
		})
	}
}

func TestContextMapLocalJSONMatchesRepomapEnvelope(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "context", "map", "--json", "--map-format", "compact"})
	var output struct {
		SchemaVersion int `json:"schema_version"`
		Totals        struct {
			Files   int `json:"files"`
			Symbols int `json:"symbols"`
		} `json:"totals"`
		Lines  []string        `json:"lines"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(got), &output); err != nil {
		t.Fatalf("invalid local map JSON: %v\n%s", err, got)
	}
	if output.SchemaVersion != 2 || output.Totals.Files == 0 || output.Totals.Symbols == 0 || len(output.Lines) == 0 {
		t.Fatalf("incomplete local map JSON: %#v", output)
	}
	if len(output.Result) != 0 {
		t.Fatalf("local --json unexpectedly used Pan envelope: %s", got)
	}
	if joined := strings.Join(output.Lines, "\n"); !strings.Contains(joined, "verbose") || !strings.Contains(joined, "helper") {
		t.Fatalf("local --json did not preserve source verbose rendering semantics: %s", got)
	}
}

func TestContextMapStructuredJSONUsesRepomapSchema(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "context", "map", "--json-structured", "--tokens", "64", "--intent", "startup"})
	var output struct {
		SchemaVersion int `json:"schema_version"`
		Totals        struct {
			Files   int `json:"files"`
			Symbols int `json:"symbols"`
		} `json:"totals"`
		Coverage struct {
			FilesScanned int `json:"files_scanned"`
		} `json:"coverage"`
		Selection struct {
			SelectedFiles int `json:"selected_files"`
		} `json:"selection"`
		Files []struct {
			Path        string `json:"path"`
			Handle      string `json:"handle"`
			ParseMethod string `json:"parse_method"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(got), &output); err != nil {
		t.Fatalf("invalid structured JSON: %v\n%s", err, got)
	}
	if output.SchemaVersion != 2 || output.Totals.Files == 0 || output.Totals.Symbols == 0 || output.Coverage.FilesScanned == 0 {
		t.Fatalf("incomplete structured JSON: %#v", output)
	}
	if len(output.Files) != output.Selection.SelectedFiles || len(output.Files) == 0 {
		t.Fatalf("file selection mismatch: %#v", output)
	}
	if output.Files[0].Handle != "file:"+output.Files[0].Path || output.Files[0].ParseMethod != "go_ast" {
		t.Fatalf("structured file = %#v", output.Files[0])
	}
}

func TestContextTaskEmitsGoalPacket(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "task", "trace startup"})
	for _, want := range []string{`"goal": "trace startup"`, `"strategy"`, `"max_tokens"`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("task output missing %q:\n%s", want, got)
		}
	}
}

func TestContextFindResolvesSymbol(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "find", "Run"})
	var matches []struct {
		Symbol struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"symbol"`
		Basis string `json:"basis"`
	}
	if err := json.Unmarshal([]byte(got), &matches); err != nil {
		t.Fatalf("find JSON is not a raw array: %v\n%s", err, got)
	}
	if len(matches) == 0 {
		t.Fatalf("find JSON returned no matches: %s", got)
	}
	for _, want := range []string{`"name": "Run"`, `"kind": "function"`, `"basis": "exact"`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("find output missing %q:\n%s", want, got)
		}
	}
}

func TestContextFindJSONUsesRepomapRawArrayAndEmptySlice(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "context", "find", "not-present", "--format", "json"})
	if strings.TrimSpace(got) != "[]" {
		t.Fatalf("empty find JSON = %s, want []", got)
	}
}

func TestContextSymbolIncludesSourceExcerpt(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "symbol", "Run", "--lines", "20", "--calls"})
	for _, want := range []string{`"query": "Run"`, "func Run()", `"callers"`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("symbol output missing %q:\n%s", want, got)
		}
	}
}

func TestContextSymbolBoundsTextAndSupportsSourceAlias(t *testing.T) {
	t.Parallel()
	text := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "text", "context", "symbol", "Run", "--max-output-lines", "2", "--calls"})
	if !bytes.Contains([]byte(text), []byte("[Output truncated:")) {
		t.Fatalf("bounded text output missing truncation notice:\n%s", text)
	}
	json := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "symbol", "Run", "--max-source-lines", "1"})
	if count := bytes.Count([]byte(json), []byte(`"number":`)); count != 1 {
		t.Fatalf("--max-source-lines source entries = %d, want 1:\n%s", count, json)
	}
	if bytes.Contains([]byte(json), []byte(`"callers": [`)) {
		t.Fatalf("callers were emitted without --calls:\n%s", json)
	}
}

func TestContextExplainReportsRankEvidence(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "explain", "internal/service/service.go"})
	for _, want := range []string{`"parse_method": "go_ast"`, `"score"`, `"detail_level": 2`, `"symbol_count"`, `"symbols"`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("explain output missing %q:\n%s", want, got)
		}
	}
}

func TestContextExplainTinyBudgetReportsOmission(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "explain", "internal/service/service.go", "--tokens", "1"})
	if !bytes.Contains([]byte(got), []byte(`"symbol_count"`)) || !bytes.Contains([]byte(got), []byte(`"omitted_reason"`)) {
		t.Fatalf("tiny explain lost bounded evidence:\n%s", got)
	}
	if bytes.Contains([]byte(got), []byte(`"symbols": [`)) {
		t.Fatalf("tiny explain unexpectedly emitted symbols:\n%s", got)
	}
}

func TestContextEndpointResolvesRoute(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", httpGoRepo(), "--format", "json", "context", "endpoint", "/health"})
	for _, want := range []string{
		`"method": "GET"`,
		`"pattern": "/health"`,
		`"handler": "healthHandler"`,
		`"framework": "net/http"`,
		`"name": "healthHandler"`,
	} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("endpoint output missing %q:\n%s", want, got)
		}
	}
}

func TestContextEndpointListsRoutesWhenRouteIsOmitted(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", httpGoRepo(), "--format", "json", "context", "endpoint"})
	for _, want := range []string{`"method": "GET"`, `"pattern": "/health"`, `"handler": "healthHandler"`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("route listing missing %q:\n%s", want, got)
		}
	}
}

func TestContextEndpointBoundsOnlyRouteText(t *testing.T) {
	t.Parallel()
	limited := runJSON(t, []string{"--repo", httpGoRepo(), "--format", "text", "context", "endpoint", "/health", "--max-output-lines", "2"})
	if !strings.Contains(limited, "[Output truncated: showing 2 of ") {
		t.Fatalf("bounded endpoint text missing truncation notice:\n%s", limited)
	}

	unlimited := runJSON(t, []string{"--repo", httpGoRepo(), "--format", "text", "context", "endpoint", "/health", "--max-output-lines", "0"})
	if strings.Contains(unlimited, "[Output truncated:") || len(unlimited) <= len(limited) {
		t.Fatalf("zero endpoint limit did not preserve full text:\n%s", unlimited)
	}

	json := runJSON(t, []string{"--repo", httpGoRepo(), "--format", "json", "context", "endpoint", "/health", "--max-output-lines", "1"})
	if strings.Contains(json, "[Output truncated:") || !strings.Contains(json, `"pattern": "/health"`) {
		t.Fatalf("endpoint JSON was truncated or incomplete:\n%s", json)
	}

	list := runJSON(t, []string{"--repo", httpGoRepo(), "--format", "text", "context", "endpoint", "--max-output-lines", "1"})
	if strings.Contains(list, "[Output truncated:") || !strings.Contains(list, "/health") {
		t.Fatalf("endpoint list text should ignore route-detail cap:\n%s", list)
	}
}

func TestContextBriefIncludesVerificationStateAndRenderedMap(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "--format", "text", "context", "brief", "--detail", "evidence"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Verify", "go build ./...", "## State", "## Map", "service.go"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("brief output missing %q:\n%s", want, out.String())
		}
	}
}

func TestContextBriefJSONIncludesVerificationStateAndMap(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "context", "brief", "--detail", "evidence"})
	for _, want := range []string{`"verify"`, `"build": "go build ./..."`, `"state"`, `"map"`, `"used_tokens"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("brief JSON missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, `"omitted_fields": []`) {
		t.Fatalf("evidence brief omitted_fields was not a JSON empty array:\n%s", got)
	}
	for _, want := range []string{`"limits": []`, `"skipped": []`, `"warnings": []`, `"confidence": "high"`, `"truncated": false`, `"truncations": []`} {
		if !strings.Contains(got, want) {
			t.Fatalf("brief JSON missing explicit answer-ready field %q:\n%s", want, got)
		}
	}
}

func TestRetrievalValidationRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "zero map tokens", args: []string{"context", "map", "--tokens", "0"}},
		{name: "negative map tokens", args: []string{"context", "map", "--tokens", "-1"}},
		{name: "blank task goal", args: []string{"context", "task", " "}},
		{name: "negative task tokens", args: []string{"context", "task", "--tokens", "-1", "goal"}},
		{name: "blank find query", args: []string{"context", "find", ""}},
		{name: "negative find top", args: []string{"context", "find", "--top", "-1", "Run"}},
		{name: "blank symbol query", args: []string{"context", "symbol", ""}},
		{name: "negative symbol lines", args: []string{"context", "symbol", "--lines", "-1", "Run"}},
		{name: "zero symbol source alias", args: []string{"context", "symbol", "--max-source-lines", "0", "Run"}},
		{name: "negative symbol output bytes", args: []string{"context", "symbol", "--max-output-bytes", "-1", "Run"}},
		{name: "blank explain file", args: []string{"context", "explain", " "}},
		{name: "blank endpoint route", args: []string{"context", "endpoint", " "}},
		{name: "negative endpoint output lines", args: []string{"context", "endpoint", "--max-output-lines", "-1"}},
		{name: "unknown map mode", args: []string{"context", "map", "--mode", "verbose"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := cli.Run(context.Background(), tt.args, newTestDeps(&out))
			if err == nil {
				t.Fatalf("args %v must fail validation", tt.args)
			}
			if code := cli.ExitCode(err); code != cli.ExitFailure {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, cli.ExitFailure, err)
			}
		})
	}
}
