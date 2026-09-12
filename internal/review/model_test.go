package review

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestModelScoreUsesBoundedCacheAndNoCacheBypassesIt(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing provider authorization")
		}
		_, _ = w.Write([]byte(`{"model":"test","choices":[{"message":{"role":"assistant","content":"[{\"index\":0,\"score\":5,\"summary\":\"high\",\"reasons\":[\"fake evidence\"]}]"}}]}`))
	}))
	defer server.Close()
	t.Setenv("PAN_REVIEW_TEST_KEY", "test-key")
	report := Report{ReadQueue: []ReadItem{{Rank: 1, Path: "internal/auth.go", Score: 11, Why: []string{"risk:security"}}}}
	opts := ModelOptions{Model: "test", BaseURL: server.URL + "/v1", APIKeyEnv: "PAN_REVIEW_TEST_KEY", CacheDir: t.TempDir()}
	first, err := opts.Score(context.Background(), report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := opts.Score(context.Background(), report)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || first.ReadQueue[0].Score != 5 || second.ReadQueue[0].Score != 5 {
		t.Fatalf("calls=%d first=%#v second=%#v", calls.Load(), first.ReadQueue, second.ReadQueue)
	}
	if _, statErr := os.Stat(filepath.Join(opts.CacheDir, "scores.json")); statErr != nil {
		t.Fatalf("persisted cache: %v", statErr)
	}
	opts.ContentHashes = map[string]string{"internal/auth.go": "changed-content"}
	if _, scoreErr := opts.Score(context.Background(), report); scoreErr != nil || calls.Load() != 2 {
		t.Fatalf("content hash must invalidate cache: calls=%d err=%v", calls.Load(), scoreErr)
	}
	_, err = ModelOptions{Model: "test", BaseURL: server.URL + "/v1", APIKeyEnv: "PAN_REVIEW_TEST_KEY", NoCache: true, CacheDir: opts.CacheDir}.Score(context.Background(), report)
	if err != nil || calls.Load() != 3 {
		t.Fatalf("no-cache calls=%d err=%v", calls.Load(), err)
	}
}

func TestModelOptionsRequireEndpointAndResolveLocal(t *testing.T) {
	if _, err := (ModelOptions{Model: "test"}).Resolve(); err == nil {
		t.Fatal("missing endpoint accepted")
	}
	got, err := (ModelOptions{Local: true}).Resolve()
	if err != nil || got.Model != localModel || got.BaseURL != localBaseURL || got.APIKeyEnv != localAPIKeyEnv {
		t.Fatalf("local profile = %#v, %v", got, err)
	}
}
