package provider

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestTransportCategoriesDoNotExposeRequestOrRetry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		cause    error
		category string
	}{
		{"dns", &net.DNSError{Err: "private fixture detail", Name: "secret.example"}, "dns"},
		{"refused", syscall.ECONNREFUSED, "connection_refused"},
		{"closed", io.ErrUnexpectedEOF, "connection_closed"},
		{"other", errors.New("https://user:private-secret@secret.example/?key=private-secret"), "network"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client, err := New(Config{Provider: "openai", Model: "fixture", APIKey: "private-secret", ProviderMaxRetries: 3, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, &url.Error{Op: "Post", URL: "https://secret.example/?key=private-secret", Err: tc.cause}
			})}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Complete(t.Context(), Request{Messages: []Message{{Role: "user", Content: "fixture"}}})
			var categorized *TransportError
			if !errors.As(err, &categorized) || categorized.Category != tc.category {
				t.Fatalf("category: %v", err)
			}
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "secret.example") || !strings.Contains(err.Error(), "submission outcome unknown") || calls.Load() != 1 {
				t.Fatalf("unsafe transport behavior: %v calls=%d", err, calls.Load())
			}
		})
	}
}
