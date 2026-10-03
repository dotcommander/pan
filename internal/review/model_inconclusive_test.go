package review

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dotcommander/pan/internal/provider"
)

func TestScoreBatchMarksAbstentionsInconclusive(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"test","choices":[{"message":{"role":"assistant","content":"[{\"index\":0,\"score\":5,\"summary\":\"high\",\"reasons\":[\"decisive\"]},{\"index\":2,\"score\":9,\"summary\":\"over\"}]"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	client, err := provider.New(provider.Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	o := ModelOptions{Model: "test", BaseURL: server.URL + "/v1"}
	rows := []ReadItem{
		{Rank: 1, Path: "a.go", Score: 7, Why: []string{"risk:x"}},
		{Rank: 2, Path: "b.go", Score: 8, Why: []string{"risk:y"}},
		{Rank: 3, Path: "c.go", Score: 9},
	}
	cache := modelScoreCache{entries: map[string]cachedModelScore{}}
	if err := o.scoreBatch(context.Background(), client, rows, &cache); err != nil {
		t.Fatal(err)
	}
	if rows[0].Score != 7 || rows[0].ModelVerdict == nil || rows[0].ModelVerdict.Status != "success" || rows[0].ModelVerdict.Score != 5 {
		t.Fatalf("decisive row not applied: %#v", rows[0])
	}
	abstentions := []struct {
		path   string
		score  int
		detail string
	}{
		{"b.go", 8, "no score returned for this row"},
		{"c.go", 9, "score 9 outside 1-5"},
	}
	for index, want := range abstentions {
		row := rows[index+1]
		if row.Score != want.score {
			t.Errorf("%s deterministic score = %d, want %d", row.Path, row.Score, want.score)
		}
		if row.ModelVerdict == nil || row.ModelVerdict.Status != "inconclusive" || row.ModelVerdict.Detail != want.detail {
			t.Errorf("%s verdict = %#v", row.Path, row.ModelVerdict)
		}

	}
	if len(cache.entries) != 1 {
		t.Fatalf("abstentions must not be cached, entries = %d", len(cache.entries))
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
}

func TestScoreBatchDegradesProviderFailureAsModelError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client, err := provider.New(provider.Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	o := ModelOptions{Model: "test", BaseURL: server.URL + "/v1"}
	rows := []ReadItem{{Rank: 1, Path: "a.go", Score: 6}}
	cache := modelScoreCache{entries: map[string]cachedModelScore{}}
	if err := o.scoreBatch(context.Background(), client, rows, &cache); err != nil {
		t.Fatalf("provider failure must degrade, not fail: %v", err)
	}
	if rows[0].Score != 6 {
		t.Fatalf("deterministic score = %d, want 6", rows[0].Score)
	}
	if rows[0].ModelVerdict == nil || rows[0].ModelVerdict.Status != "error" {
		t.Fatalf("missing error verdict: %#v", rows[0])
	}
}
