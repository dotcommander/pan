package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestImportToolReportEnvelope(t *testing.T) {
	t.Parallel()
	data, err := ImportToolReport(t.Context(), "ast-grep", strings.NewReader("[]"), "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Schema   string   `json:"schema_version"`
		Command  []string `json:"command"`
		Analysis struct {
			Complete bool `json:"complete"`
		} `json:"analysis"`
		Result struct {
			Schema string `json:"schema"`
			Source struct {
				Coverage string `json:"coverage"`
			} `json:"source"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != "pan/v1" || !result.Analysis.Complete || result.Result.Schema != "tool-report/v1" || result.Result.Source.Coverage != "unknown" {
		t.Fatalf("incorrect envelope: %s", data)
	}
	if bytes.Contains(data, []byte(`"snapshot":{`)) {
		t.Fatal("invented snapshot")
	}
	text, err := ImportToolReport(t.Context(), "ast-grep", strings.NewReader("[]"), "text")
	if err != nil || len(text) == 0 {
		t.Fatalf("text output: %v", err)
	}
}

func TestImportToolReportFailsWithoutOutput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"bad", `[{"file":"../a"}]`} {
		data, err := ImportToolReport(t.Context(), "ast-grep", strings.NewReader(input), "json")
		if err == nil || len(data) != 0 {
			t.Fatal("invalid input returned output")
		}
	}
	b := reportBuffer{ctx: t.Context(), limit: 3}
	if _, err := b.Write([]byte("four")); err == nil || b.Len() != 0 {
		t.Fatal("output cap failed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b = reportBuffer{ctx: ctx, limit: 3}
	if _, err := b.Write([]byte("a")); !errors.Is(err, context.Canceled) {
		t.Fatalf("writer cancellation: %v", err)
	}
}

func TestImportOutputPreflightIncludesPrettyEnvelope(t *testing.T) {
	t.Parallel()
	// Compact imported evidence fits 2500 bytes, while the indented report and
	// envelope exceed it. Both formats must fail before render.Write is called.
	input := `{"statistics":{},"duplicates":[],"extra":[` + strings.Repeat(`{},`, 200) + `{}]}`
	for _, format := range []string{"json", "text"} {
		data, err := importToolReportBounded(t.Context(), "jscpd", strings.NewReader(input), format, 2500)
		if err == nil || len(data) != 0 || !strings.Contains(err.Error(), "output byte limit") || strings.Contains(err.Error(), "render imported report:") {
			t.Fatalf("unbounded pretty output (%s): %v", format, err)
		}
	}
}
