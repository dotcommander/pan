package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompletionToolContract(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Errorf("unexpected request path/method/auth")
		}
		var request Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "fixture-model" || len(request.Tools) != 1 || request.Tools[0].Function.Name != "submit" {
			t.Errorf("unexpected request: %+v", request)
		}
		_, _ = io.WriteString(w, `{"id":"receipt","model":"actual-model","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"submit","arguments":"{\"ok\":true}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":9}}`)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL + "/v1/", APIKey: "fixture-key", Model: "fixture-model"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}, Tools: []Tool{{Type: "function", Function: FunctionDefinition{Name: "submit", Parameters: map[string]any{"type": "object"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "actual-model" || result.Usage.TotalTokens != 9 || result.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"ok":true}` {
		t.Fatalf("lost response contract: %+v", result)
	}
}

func TestFailureIsBoundedAndNotRetried(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		limit  int64
		want   string
	}{
		{"http", 429, "secret fixture-key", 100, "HTTP 429"},
		{"size", 200, strings.Repeat("x", 101), 100, "byte limit"},
		{"malformed", 200, "secret fixture-key", 100, "invalid completion JSON"},
		{"empty", 200, `{"choices":[]}`, 100, "no completion choices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Model: "fixture", MaxResponseBytes: tc.limit})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}})
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "fixture-key") {
				t.Fatalf("unexpected error: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("got %d attempts", calls.Load())
			}
		})
	}
}

func TestHTTPFailureReportsBoundedRedactedProviderDetail(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"status":"INVALID_ARGUMENT","message":"field tools rejected fixture-key `+strings.Repeat("x", 600)+`"}}`)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Model: "fixture", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}})
	var status *HTTPError
	if !errors.As(err, &status) || !strings.Contains(err.Error(), "INVALID_ARGUMENT: field tools rejected [redacted]") {
		t.Fatalf("missing provider detail: %v", err)
	}
	if strings.Contains(err.Error(), "fixture-key") || len(status.Detail) > 515 {
		t.Fatalf("unbounded or secret detail: %q", status.Detail)
	}
}

func TestRedirectDoesNotReplayCredentials(t *testing.T) {
	t.Parallel()
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Model: "fixture", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}})
	var status *HTTPError
	if !errors.As(err, &status) || status.StatusCode != 307 || leaked.Load() != 0 {
		t.Fatalf("redirect replay/error: %d %v", leaked.Load(), err)
	}
}

func TestCancellationReachesTransport(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	client, err := New(Config{BaseURL: server.URL, Model: "fixture", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, callErr := client.Complete(ctx, Request{Messages: []Message{{Role: "user", Content: "test"}}})
		finished <- callErr
	}()
	<-entered
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestEndpointValidation(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"relative", "file:///tmp/test", "https://user:password@example.test", "https://example.test?key=secret", "https://example.test#fragment"} {
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			if _, err := New(Config{BaseURL: base}); err == nil {
				t.Fatal("accepted invalid endpoint")
			}
		})
	}
}
