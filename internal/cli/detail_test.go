package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestBriefDetailProjections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		must []string
		not  []string
	}{
		{name: "compact default", args: []string{"context", "brief"}, must: []string{`"detail": "compact"`, `"omitted_fields"`, `"top_files"`}, not: []string{`"map":`, `"instructions":`, `"components":`, `"families":`}},
		{name: "evidence", args: []string{"context", "brief", "--detail", "evidence"}, must: []string{`"detail": "evidence"`, `"map"`, `"used_tokens"`, `"instructions"`}},
		{name: "paths", args: []string{"context", "brief", "--detail", "paths"}, must: []string{`"detail": "paths"`, `"paths"`, `"analysis"`, `"budget_info"`}, not: []string{`"top_files":`, `"map":`, `"instructions":`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runJSON(t, append([]string{"--repo", basicGoRepo(), "--format", "json"}, tt.args...))
			for _, want := range tt.must {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tt.not {
				if strings.Contains(got, unwanted) {
					t.Fatalf("unexpected %q:\n%s", unwanted, got)
				}
			}
		})
	}
}

func TestBriefPathsPlainAndAliasParity(t *testing.T) {
	t.Parallel()
	var contextOut, aliasOut bytes.Buffer
	for _, tc := range []struct {
		args []string
		out  *bytes.Buffer
	}{
		{[]string{"context", "brief", "--detail", "paths"}, &contextOut},
		{[]string{"brief", "--detail", "paths"}, &aliasOut},
	} {
		if err := cli.Run(context.Background(), append([]string{"--repo", basicGoRepo(), "--format", "text"}, tc.args...), newTestDeps(tc.out)); err != nil {
			t.Fatal(err)
		}
	}
	if contextOut.String() != aliasOut.String() || !strings.Contains(contextOut.String(), "internal/service/service.go\n") {
		t.Fatalf("path outputs differ or lack ranked path:\ncontext=%q\nalias=%q", contextOut.String(), aliasOut.String())
	}
}

func TestBriefDetailRejectsMixedCase(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "--format", "json", "brief", "--detail", "Evidence"}, newTestDeps(&out))
	if err == nil {
		t.Fatalf("mixed-case detail unexpectedly accepted: %s", out.String())
	}
}

func TestBriefOmittedFieldsSorted(t *testing.T) {
	t.Parallel()
	got := runJSON(t, []string{"--repo", basicGoRepo(), "--format", "json", "brief"})
	var envelope struct {
		Result struct {
			Omitted []string `json:"omitted_fields"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(envelope.Result.Omitted); i++ {
		if envelope.Result.Omitted[i-1] > envelope.Result.Omitted[i] {
			t.Fatalf("omitted fields unsorted: %#v", envelope.Result.Omitted)
		}
	}
}
