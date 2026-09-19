package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelEchoMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		requested, served string
		want              bool
	}{
		{"", "", true},
		{"gpt-4o", "", true},
		{"", "gpt-4o", true},
		{"gpt-4o", "gpt-4o", true},
		{"gpt-4o", "gpt-4o-2024-08-13", true},
		{"gpt-4o", "gpt-4o-0613", true},
		{"gpt-4o", "gpt-4o:latest", true},
		{"claude-3-5-sonnet", "claude-3-5-sonnet@20240620", true},
		{"gemini-1.5-flash", "models/gemini-1.5-flash", true},
		{"gpt-4o", "openai/gpt-4o-2024-08-13", true},
		// Reroutes must fail: a different family or variant is a different
		// model, even though it shares a prefix with the request.
		{"gpt-4o", "gpt-4o-mini", false},
		{"gpt-4o", "gpt-4o:free", false},
		{"gpt-4o", "gpt-4o-turbo", false},
		{"gpt-4o", "openai/gpt-4o-mini", false},
		{"gpt-4o", "gpt-4", false},
		{"gpt-4o", "claude-3-5-sonnet", false},
		{"gpt-4o", "gpt-4o-", false},
		{" gpt-4o ", "gpt-4o", true},
	}
	for _, tc := range cases {
		if got := modelEchoMatches(tc.requested, tc.served); got != tc.want {
			t.Errorf("modelEchoMatches(%q, %q) = %v, want %v", tc.requested, tc.served, got, tc.want)
		}
	}
}

func TestDecodeRejectsServedModelReroute(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o-mini","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}})
	if err == nil || !strings.Contains(err.Error(), `served model "gpt-4o-mini"`) {
		t.Fatalf("reroute not rejected: %v", err)
	}
	var status *HTTPError
	if errors.As(err, &status) {
		t.Fatalf("echo mismatch must not be an HTTP retryable error: %v", err)
	}
}

func TestDecodeAcceptsVersionDecoratedEcho(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o-2024-08-13","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}}); err != nil {
		t.Fatalf("decorated echo must be accepted: %v", err)
	}
}

func TestHTTPErrorCapturesRequestID(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "req-123")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "test"}}})
	var status *HTTPError
	if !errors.As(err, &status) {
		t.Fatalf("want HTTPError, got %v", err)
	}
	if status.RequestID != "req-123" {
		t.Fatalf("request id = %q, want req-123", status.RequestID)
	}
	if !strings.Contains(err.Error(), "(request req-123)") {
		t.Fatalf("error message missing request id: %v", err)
	}
}

func TestRequestIDFromHeadersPrefersFirstPresent(t *testing.T) {
	t.Parallel()
	header := http.Header{}
	if got := requestIDFromHeaders(header); got != "" {
		t.Fatalf("empty header = %q, want empty", got)
	}
	header.Set("X-Goog-Request-Id", "goog-1")
	if got := requestIDFromHeaders(header); got != "goog-1" {
		t.Fatalf("goog id = %q", got)
	}
	header.Set("X-Request-Id", "openai-1")
	if got := requestIDFromHeaders(header); got != "openai-1" {
		t.Fatalf("precedence = %q, want openai-1", got)
	}
	header.Set("X-Request-Id", "   ")
	if got := requestIDFromHeaders(header); got != "goog-1" {
		t.Fatalf("blank id must be skipped, got %q", got)
	}
}
