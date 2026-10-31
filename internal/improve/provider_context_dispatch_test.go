package improve

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestProviderContextRankingOnlyScoresRankedProjections(t *testing.T) {
	t.Parallel()
	snap := analyze.Snapshot{Files: []analyze.File{
		{Path: "pkg/alpha.go", Language: analyze.LanguageGo},
		{Path: "pkg/beta.go", Language: analyze.LanguageGo},
	}}
	for _, action := range []string{providerAudit, "unknown", ""} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			if got := providerContextRanking(snap, map[string]any{providerActionKey: action}); got != nil {
				t.Fatalf("unused ranking for %q: %+v", action, got)
			}
		})
	}
	for _, action := range []string{"map", "impact", providerSymbolKey, providerBrief} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			got := providerContextRanking(snap, map[string]any{providerActionKey: action, "intent": "beta"})
			want := ranking.Rank(snap, "", ranking.Options{Intent: "beta"})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ranking changed for %q: got %+v, want %+v", action, got, want)
			}
		})
	}
}

func TestProviderContextAuditDispatchPreservesFilteredProjection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", fixtureGoMod)
	writeFile(t, root, "cmd/main.go", "package main\nfunc main() {}\n")
	writeFile(t, root, "internal/private.go", "package internal\nfunc Private() {}\n")
	reader, err := newProviderReader(root, []string{"internal/private.go"}, 40000)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := reader.repoContext(context.Background(), map[string]any{providerActionKey: providerAudit, "limit": 4})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Action    string          `json:"action"`
		Overview  json.RawMessage `json:"overview"`
		ReadQueue []struct {
			Path string `json:"path"`
		} `json:"read_queue"`
		Gates []string `json:"review_gates"`
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatal(err)
	}
	if result.Action != providerAudit || len(result.Overview) == 0 || len(result.Gates) != 2 || len(result.ReadQueue) == 0 {
		t.Fatalf("incomplete audit projection: %s", encoded)
	}
	for _, row := range result.ReadQueue {
		if row.Path == "internal/private.go" {
			t.Fatal("excluded source leaked into audit")
		}
	}
}
