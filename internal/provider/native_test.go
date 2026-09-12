package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestAnthropicNativeToolWireAndResponse(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		Provider: "anthropic", APIKey: "fixture-key", Model: "claude-fixture",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://api.anthropic.com/v1/messages" || request.Header.Get("X-API-Key") != "fixture-key" || request.Header.Get("Anthropic-Version") != "2023-06-01" || request.Header.Get("Authorization") != "" {
				t.Errorf("unexpected native request: %s %#v", request.URL, request.Header)
			}
			var payload map[string]any
			if err := decodeJSON(request.Body, &payload); err != nil {
				t.Error(err)
			}
			if payload["system"] != "rules" || payload["thinking"].(map[string]any)["type"] != "disabled" {
				t.Errorf("missing provider options: %#v", payload)
			}
			tools := payload["tools"].([]any)
			if tools[0].(map[string]any)["input_schema"] == nil {
				t.Errorf("missing Anthropic tool schema: %#v", tools)
			}
			return jsonResponse(`{"id":"msg","model":"claude-fixture","stop_reason":"tool_use","content":[{"type":"text","text":"inspect"},{"type":"tool_use","id":"call-1","name":"read","input":{"path":"a.go"}}],"usage":{"input_tokens":3,"output_tokens":4}}`), nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "go"}}, Tools: []Tool{{Type: "function", Function: FunctionDefinition{Name: "read", Parameters: map[string]any{"type": "object"}}}}, ProviderOptions: map[string]any{"thinking": map[string]any{"type": "disabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"path":"a.go"}` || response.Usage.TotalTokens != 7 {
		t.Fatalf("lost native response: %#v", response)
	}
}

func TestGeminiNativeToolResultAndThinkingWire(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		Provider: "gemini", APIKey: "fixture-key", Model: "gemini-fixture",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1beta/models/gemini-fixture:generateContent" || request.URL.Query().Get("key") != "fixture-key" || request.Header.Get("Authorization") != "" {
				t.Errorf("unexpected Gemini endpoint: %s", request.URL)
			}
			var payload map[string]any
			if err := decodeJSON(request.Body, &payload); err != nil {
				t.Error(err)
			}
			config := payload["generationConfig"].(map[string]any)
			if config["thinkingConfig"].(map[string]any)["thinkingBudget"].(float64) != 0 {
				t.Errorf("thinking remained enabled: %#v", config)
			}
			contents := payload["contents"].([]any)
			response := contents[1].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
			if response["name"] != "read" {
				t.Errorf("tool result lost name: %#v", response)
			}
			return jsonResponse(`{"candidates":[{"content":{"parts":[{"text":"done"},{"functionCall":{"name":"write","args":{"ok":true}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`), nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "system", Content: "rules"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "read-1", Type: "function", Function: FunctionCall{Name: "read", Arguments: `{}`}}}}, {Role: "tool", ToolCallID: "read-1", Content: "source"}}, ProviderOptions: map[string]any{"thinking": map[string]any{"type": "disabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Choices[0].Message.ToolCalls[0].Function.Name != "write" || response.Usage.TotalTokens != 5 {
		t.Fatalf("lost Gemini response: %#v", response)
	}
}

func TestRetryOnlyKnownHTTPRejections(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" || request.Header.Get("Authorization") != "Bearer fixture-key" || request.Header.Get("X-API-Key") != "" {
			t.Errorf("custom base did not use OpenAI-compatible request: %s %#v", request.URL, request.Header)
		}
		if calls.Add(1) == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(writer, `{"model":"fixture","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{Provider: "anthropic", BaseURL: server.URL, APIKey: "fixture-key", Model: "fixture", ProviderMaxRetries: 1, MaxProviderBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", calls.Load())
	}
}

func TestRetryPolicyOnlyAllowsProviderRejections(t *testing.T) {
	t.Parallel()
	for _, statusCode := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		if !retryableHTTPError(&HTTPError{StatusCode: statusCode}) {
			t.Errorf("status %d was not retryable", statusCode)
		}
	}
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		if retryableHTTPError(&HTTPError{StatusCode: statusCode}) {
			t.Errorf("status %d unexpectedly retryable", statusCode)
		}
	}
}

func TestRetryDoesNotRepeatUnknownTransportOutcome(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client, err := New(Config{Provider: "openai", Model: "fixture", ProviderMaxRetries: 2, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, io.ErrUnexpectedEOF
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}}); err == nil {
		t.Fatal("transport failure succeeded")
	}
	if calls.Load() != 1 {
		t.Fatalf("unknown outcome retried %d times", calls.Load())
	}
}

func decodeJSON(body io.ReadCloser, target any) error {
	defer func() { _ = body.Close() }()
	return json.NewDecoder(body).Decode(target)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(body))}
}
