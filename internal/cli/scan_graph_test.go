package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestScanGraphJSONReportsHubsAndEdges(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "--format", "json", "scan", "graph"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result struct {
			Nodes int            `json:"nodes"`
			Edges int            `json:"edges"`
			Kinds map[string]int `json:"kinds"`
			Hubs  []struct {
				ID       string `json:"id"`
				InDegree int    `json:"in_degree"`
			} `json:"hubs"`
			Cycles [][]string `json:"cycles"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("graph output must be a graph report object: %v\n%s", err, out.String())
	}
	report := envelope.Result
	if report.Nodes < 3 || report.Edges < 2 {
		t.Fatalf("fixture graph too small: nodes=%d edges=%d\n%s", report.Nodes, report.Edges, out.String())
	}
	if report.Kinds["calls"] < 2 {
		t.Fatalf("kinds must count call edges: %v", report.Kinds)
	}
	if len(report.Hubs) == 0 || report.Hubs[0].ID == "" {
		t.Fatalf("hubs must list at least one ranked node: %+v", report.Hubs)
	}
	if report.Cycles == nil {
		t.Fatal("cycles must marshal as an array, not null")
	}
}

func TestScanGraphRejectsNegativeTop(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "scan", "graph", "--top", "-1"}, newTestDeps(&out)); err == nil {
		t.Fatal("negative --top must fail validation")
	}
}
