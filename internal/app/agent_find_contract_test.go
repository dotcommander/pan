package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/agent"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/retrieval"
)

func TestAgentServeSymbolFindContract(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("source fixture integration")
	}
	for _, tc := range []struct {
		name    string
		query   string
		partial bool
		outcome string
	}{
		{name: "matches", query: "Found"},
		{name: "complete miss", query: "Missing", outcome: retrieval.FindOutcomeNoMatchInScope},
		{name: "partial miss", query: "Missing", partial: true, outcome: retrieval.FindOutcomeNoMatchInPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte("package fixture\n\nfunc Found() {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			if tc.partial {
				cfg.MaxFileBytes = 64
				content := "package fixture\n" + strings.Repeat("// oversized fixture\n", 20)
				if err := os.WriteFile(filepath.Join(root, "large.go"), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			state := New(Deps{Config: cfg, SnapshotSource: "live"}).AgentServe(root)
			var input, output bytes.Buffer
			request := map[string]any{
				"jsonrpc": agent.ServeJSONRPC, "id": 1, "method": agent.MethodSymbolFind,
				"params": map[string]string{"query": tc.query},
			}
			if err := json.NewEncoder(&input).Encode(request); err != nil {
				t.Fatal(err)
			}
			if err := agent.RunServe(t.Context(), bufio.NewReader(&input), &output, state); err != nil {
				t.Fatal(err)
			}
			var response struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int             `json:"id"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.JSONRPC != agent.ServeJSONRPC || response.ID != 1 || len(response.Error) != 0 {
				t.Fatalf("response = %#v", response)
			}
			var result struct {
				Matches  []retrieval.SymbolMatch `json:"matches"`
				Query    retrieval.FindQuery     `json:"query"`
				Outcome  string                  `json:"outcome"`
				Guidance []string                `json:"guidance"`
			}
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatalf("schema-led decode: %v; result=%s", err, response.Result)
			}
			if tc.outcome == "" {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(response.Result, &fields); err != nil {
					t.Fatal(err)
				}
				if len(fields) != 1 || len(result.Matches) != 1 || result.Matches[0].Symbol.Name != tc.query {
					t.Fatalf("match envelope = %s", response.Result)
				}
				return
			}
			if result.Matches == nil || len(result.Matches) != 0 || result.Query.Name != tc.query || result.Outcome != tc.outcome || len(result.Guidance) == 0 {
				t.Fatalf("miss envelope = %s", response.Result)
			}
			if tc.partial && !strings.Contains(strings.Join(result.Guidance, "\n"), "max_file_bytes") {
				t.Fatalf("partial miss lost analysis bounds: %s", response.Result)
			}
		})
	}
}
