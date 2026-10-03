package retrieval

import (
	"context"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"os"
	"path/filepath"
	"testing"
)

func TestQualifierOnlyFindHasNoWildcardMeaning(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"kind:func", "file:owner.go", "kind:func:file:owner.go"} {
		parsed := ParseFindQuery(query)
		if parsed.Name != "" {
			t.Fatalf("%q parsed %#v", query, parsed)
		}
		if got := Find([]ranking.RankedFile{{Symbols: []analyze.Symbol{{Name: "Owner", Kind: "func"}}}}, parsed.Name, parsed.Kind, parsed.File); len(got) != 0 {
			t.Fatalf("qualifier matched: %#v", got)
		}
	}
}

func TestRoutesUseCapturedGenerationAndAnyMethodCandidates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	captured := []byte("http.HandleFunc(\"/users\", Users)\nhttp.HandleFunc(\"GET /users\", GetUsers)\n")
	snap := analyze.Snapshot{Root: root, Files: []analyze.File{{Path: "routes.go", Language: analyze.LanguageGo}}, Captured: map[string][]byte{"routes.go": captured}}
	if err := os.WriteFile(filepath.Join(root, "routes.go"), []byte("http.HandleFunc(\"/changed\", Changed)"), 0600); err != nil {
		t.Fatal(err)
	}
	routes, err := Routes(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if got := matchRoutes(routes, "GET /users"); len(got) != 2 {
		t.Fatalf("GET candidates %#v", got)
	}
	if got := matchRoutes(routes, "POST /users"); len(got) != 1 || got[0].Method != "ANY" {
		t.Fatalf("POST candidates %#v", got)
	}
	if got := matchRoutes(routes, "/changed"); len(got) != 0 {
		t.Fatal("live source escaped capture")
	}
	snap.Captured = nil
	if _, err := Routes(context.Background(), snap); err == nil {
		t.Fatal("missing capture should fail")
	}
}
