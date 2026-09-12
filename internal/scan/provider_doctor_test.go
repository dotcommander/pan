package scan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderDoctorLoopback(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected request: %s auth=%t", r.URL.Path, r.Header.Get("Authorization") != "")
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(server.Close)
	result, err := CheckProvider(context.Background(), "fake", server.URL+"/v1", "PATH")
	if err != nil || !result.Attempted || result.Status != "reachable" || !result.APIKeyEnvPresent {
		t.Fatalf("health: %+v, %v", result, err)
	}
}

func TestProviderDoctorRefusals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, endpoint, status string }{
		{"remote", "http://192.0.2.1/v1", "refused_non_loopback"},
		{"credentials", "http://user:secret@127.0.0.1/v1", doctorUnsafeEndpoint},
		{"scheme", "file:///models", doctorUnsafeEndpoint},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := CheckProvider(context.Background(), "fake", test.endpoint, "")
			if err != nil || result.Attempted || result.Status != test.status {
				t.Fatalf("health: %+v, %v", result, err)
			}
		})
	}
}

func TestProviderDoctorDoesNotFollowRedirect(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.0.2.1/models", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	result, err := CheckProvider(context.Background(), "fake", server.URL, "")
	if err != nil || result.Status != doctorUnreachable {
		t.Fatalf("health: %+v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CheckProvider(ctx, "fake", server.URL, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
