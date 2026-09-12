package serve

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testSpec = "title: %s\nphases:\n  - name: Only\n    kind: Pass\n    stages:\n      - { label: in }\n"

func TestRunServesDirectoryCollection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSpec(t, filepath.Join(dir, "first.yaml"), "First")
	writeSpec(t, filepath.Join(dir, "second.yaml"), "Second")

	ctx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{Paths: []string{dir}, Port: 0, Stdout: io.Discard, OpenBrowser: func(url string) { urlCh <- url }})
	}()
	t.Cleanup(func() {
		cancel()
		awaitRun(t, errCh)
	})

	baseURL := awaitURL(t, urlCh, errCh)
	if !strings.HasPrefix(baseURL, "http://127.0.0.1:") {
		t.Fatalf("default bind = %q, want loopback address", baseURL)
	}
	collection := collectionName(dir)
	mustContainURL(t, baseURL, "First")
	mustContainURL(t, baseURL+collection, "Second")
	mustContainURL(t, baseURL+collection+"/first", "First")
}

func TestRunServesExplicitFileCollection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "only.yaml")
	writeSpec(t, path, "Only")

	ctx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{Paths: []string{path}, Port: 0, Stdout: io.Discard, OpenBrowser: func(url string) { urlCh <- url }})
	}()
	t.Cleanup(func() {
		cancel()
		awaitRun(t, errCh)
	})

	baseURL := awaitURL(t, urlCh, errCh)
	mustContainURL(t, baseURL, "Only")
	mustContainURL(t, baseURL+"only", "Only")
	mustContainURL(t, baseURL+"only.html", "Only")
}

func TestRefreshUpdatesAddsPreservesAndRemoves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := filepath.Join(dir, "first.yaml")
	second := filepath.Join(dir, "second.yaml")
	writeSpec(t, first, "First")
	plans, err := expandPaths([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	set := newCollectionSet()
	states, err := loadPlans(set, plans)
	if err != nil {
		t.Fatal(err)
	}
	collection := collectionName(dir)

	writeSpec(t, first, "Reloaded")
	_ = refresh(set, plans, states)
	mustContain(t, servePage(set, "/"+collection+"/first"), "Reloaded")

	writeSpec(t, second, "Second")
	_ = refresh(set, plans, states)
	mustContain(t, servePage(set, "/"+collection+"/second"), "Second")

	if err := os.WriteFile(first, []byte("title: [partial write"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = refresh(set, plans, states)
	mustContain(t, servePage(set, "/"+collection+"/first"), "Reloaded")

	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	_ = refresh(set, plans, states)
	if status := serveStatus(set, "/"+collection+"/first"); status != http.StatusNotFound {
		t.Fatalf("deleted spec status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestRefreshKeepsLastGoodCollectionOnSpecCollision(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "shared.yaml")
	ymlPath := filepath.Join(dir, "shared.yml")
	writeSpec(t, yamlPath, "YAML version")
	plans, err := expandPaths([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	set := newCollectionSet()
	states, err := loadPlans(set, plans)
	if err != nil {
		t.Fatal(err)
	}
	pagePath := "/" + collectionName(dir) + "/shared"

	writeSpec(t, ymlPath, "YML version")
	states = refresh(set, plans, states)
	body := servePage(set, pagePath)
	mustContain(t, body, "YAML version")
	if strings.Contains(body, "YML version") {
		t.Fatal("collision replaced the last-good page")
	}

	if err := os.Remove(yamlPath); err != nil {
		t.Fatal(err)
	}
	_ = refresh(set, plans, states)
	mustContain(t, servePage(set, pagePath), "YML version")
}

func writeSpec(t *testing.T, path, title string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(fmt.Sprintf(testSpec, title)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func awaitURL(t *testing.T, urls <-chan string, errs <-chan error) string {
	t.Helper()
	select {
	case url := <-urls:
		return url
	case err := <-errs:
		t.Fatalf("Run before startup: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not report its startup URL")
	}
	return ""
}

func awaitRun(t *testing.T, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

func mustContain(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("response did not contain %q", want)
	}
}

func mustContainURL(t *testing.T, url, want string) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", url, response.StatusCode, http.StatusOK)
	}
	mustContain(t, string(body), want)
}

func servePage(set *collectionSet, path string) string {
	recorder := httptest.NewRecorder()
	makeHandler(set).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Body.String()
}

func serveStatus(set *collectionSet, path string) int {
	recorder := httptest.NewRecorder()
	makeHandler(set).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Code
}
