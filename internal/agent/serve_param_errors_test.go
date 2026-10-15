package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
)

type paramErrorBackend struct {
	serveTestBackend
	calls int
}

func (b *paramErrorBackend) AgentSymbolFind(ctx context.Context, query string) (any, error) {
	b.calls++
	return b.serveTestBackend.AgentSymbolFind(ctx, query)
}

func (b *paramErrorBackend) AgentFileExplain(ctx context.Context, path string) (any, error) {
	b.calls++
	return b.serveTestBackend.AgentFileExplain(ctx, path)
}

func (b *paramErrorBackend) AgentFileContext(ctx context.Context, query, kind, file string, lines int) (any, error) {
	b.calls++
	return b.serveTestBackend.AgentFileContext(ctx, query, kind, file, lines)
}

func TestRunServeParameterErrorsPreserveCauseAndRecover(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		method  string
		params  any
		message string
	}{
		{"symbol wrong query type", MethodSymbolFind, map[string]any{"query": true}, "invalid params"},
		{"symbol non-object params", MethodSymbolFind, []string{"Main"}, "params must be an object"},
		{"symbol missing query", MethodSymbolFind, map[string]any{}, "query must not be empty"},
		{"symbol empty query", MethodSymbolFind, map[string]any{"query": ""}, "query must not be empty"},
		{"explain wrong path type", MethodFileExplain, map[string]any{"path": true}, "invalid params"},
		{"explain missing path", MethodFileExplain, map[string]any{}, "path must not be empty"},
		{"explain empty path", MethodFileExplain, map[string]any{"path": ""}, "path must not be empty"},
		{"context wrong query type", MethodFileContext, map[string]any{"query": true}, "invalid params"},
		{"context wrong kind type", MethodFileContext, map[string]any{"query": "Main", "kind": true}, "invalid params"},
		{"context wrong file type", MethodFileContext, map[string]any{"query": "Main", "file": true}, "invalid params"},
		{"context wrong line type", MethodFileContext, map[string]any{"query": "Main", "max_source_lines": "32"}, "invalid params"},
		{"context fractional lines", MethodFileContext, map[string]any{"query": "Main", "max_source_lines": 1.5}, "invalid params"},
		{"context missing query", MethodFileContext, map[string]any{}, "query must not be empty"},
		{"context empty query", MethodFileContext, map[string]any{"query": ""}, "query must not be empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			valid := map[string]any{"query": "Main"}
			if tc.method == MethodFileExplain {
				valid = map[string]any{"path": "a.go"}
			}
			var input, output bytes.Buffer
			for _, request := range []map[string]any{
				{"jsonrpc": ServeJSONRPC, "id": "invalid", "method": tc.method, "params": tc.params},
				{"jsonrpc": ServeJSONRPC, "id": "recovered", "method": tc.method, "params": valid},
			} {
				if err := json.NewEncoder(&input).Encode(request); err != nil {
					t.Fatal(err)
				}
			}
			backend := &paramErrorBackend{}
			if err := RunServe(t.Context(), bufio.NewReader(&input), &output, backend); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(&output)
			for _, id := range []string{"invalid", "recovered"} {
				var response struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      string          `json:"id"`
					Result  json.RawMessage `json:"result"`
					Error   *struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := decoder.Decode(&response); err != nil {
					t.Fatal(err)
				}
				if response.JSONRPC != ServeJSONRPC || response.ID != id {
					t.Fatalf("identity = %#v", response)
				}
				if id == "invalid" {
					if response.Error == nil || response.Error.Code != ServeCodeInvalidParams || response.Error.Message != tc.message || len(response.Result) != 0 {
						t.Fatalf("invalid response = %#v, error = %+v; want %q", response, response.Error, tc.message)
					}
				} else if response.Error != nil || len(response.Result) == 0 {
					t.Fatalf("recovery response = %#v", response)
				}
			}
			if backend.calls != 1 {
				t.Fatalf("backend calls = %d, want only valid request", backend.calls)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("extra response = %#v, error = %v", extra, err)
			}
		})
	}
}
