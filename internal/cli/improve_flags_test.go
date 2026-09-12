package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestImproveStatsRecentFlag(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	deps := newImproveDeps(t, &out)
	repo := writeImproveFixture(t, map[string]string{"go.mod": fixtureGoMod})
	if err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "stats", "--recent", "2"}, deps); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"recent_window": 2`)) {
		t.Fatalf("recent flag was not applied: %s", out.String())
	}
}

func TestImproveProbeProviderUsesBearerAPIKeyByDefault(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"rationale\":\"none\",\"changes\":[]}"}}]}`)
	}))
	defer server.Close()
	t.Setenv("PAN_IMPROVE_TEST_API_KEY", "fixture-key")
	var out bytes.Buffer
	repo := writeImproveFixture(t, map[string]string{"go.mod": fixtureGoMod, "demo.go": "package demo\n"})
	if err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "probe", "--provider-base-url", server.URL, "--provider-api-key-env", "PAN_IMPROVE_TEST_API_KEY", "--provider-model", "fake"}, newImproveDeps(t, &out)); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer fixture-key" {
		t.Fatalf("Authorization = %q, want bearer API key", gotAuth)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatal(err)
	}
}

func TestImproveProbeLoadsProviderSettingsFromConfig(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"rationale\":\"none\",\"changes\":[]}"}}]}`)
	}))
	defer server.Close()
	t.Setenv("PAN_IMPROVE_CONFIG_KEY", "fixture-key")
	configPath := t.TempDir() + "/improve.yaml"
	if err := os.WriteFile(configPath, []byte("base_url: "+server.URL+"\napi_key_env: PAN_IMPROVE_CONFIG_KEY\nmodel: fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	repo := writeImproveFixture(t, map[string]string{"go.mod": fixtureGoMod, "demo.go": "package demo\n"})
	if err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "improve", "probe", "--config", configPath}, newImproveDeps(t, &out)); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer fixture-key" {
		t.Fatalf("Authorization = %q, want bearer API key from config", gotAuth)
	}
}
