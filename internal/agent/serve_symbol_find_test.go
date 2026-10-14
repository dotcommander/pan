package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

type symbolFindServeBackend struct {
	serveTestBackend
	result any
}

func (b *symbolFindServeBackend) AgentSymbolFind(context.Context, string) (any, error) {
	return b.result, nil
}

func TestRunServeSymbolFindPreservesResultEnvelope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		result any
	}{
		{name: "matches", result: map[string]any{"matches": []string{"Found"}}},
		{name: "complete miss", result: map[string]any{
			"query": map[string]string{"name": "Missing"}, "matches": []string{},
			"outcome": "no_match_in_analyzed_scope", "guidance": []string{"check spelling"},
		}},
		{name: "partial miss", result: map[string]any{
			"query": map[string]string{"name": "Missing"}, "matches": []string{},
			"outcome": "no_match_in_partial_snapshot", "guidance": []string{"inspect analysis limits"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var input, output bytes.Buffer
			request := map[string]any{
				"jsonrpc": ServeJSONRPC, "id": 1, "method": MethodSymbolFind,
				"params": map[string]string{"query": "Missing"},
			}
			if err := json.NewEncoder(&input).Encode(request); err != nil {
				t.Fatal(err)
			}
			backend := &symbolFindServeBackend{result: tc.result}
			if err := RunServe(t.Context(), bufio.NewReader(&input), &output, backend); err != nil {
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
			if response.JSONRPC != ServeJSONRPC || response.ID != 1 || len(response.Error) != 0 {
				t.Fatalf("response = %#v", response)
			}
			want, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(response.Result, want) {
				t.Fatalf("result = %s, want %s", response.Result, want)
			}
		})
	}
}
