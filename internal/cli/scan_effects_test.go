package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestScanEffectsPathsOnlyFiltersBeforeCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 13; i++ {
		lines = append(lines, "func read() { os.Open(\"p\") }")
	}
	lines = append(lines, "func request() { http.Get(\"https://example.test\") }")
	if err := os.WriteFile(filepath.Join(dir, "effects.go"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"--repo", dir, "scan", "effects", "--kind", "http", "--paths-only"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "effects.go\n" {
		t.Fatalf("paths-only output = %q", got)
	}
}

func TestScanEffectsPathsOnlyJSONIsRawPathArray(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "effects.go"), []byte("package effects\nfunc request() { http.Get(\"https://example.test\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"--repo", dir, "--format", "json", "scan", "effects", "--paths-only"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "[\n  \"effects.go\"\n]") || strings.Contains(got, "schema_version") {
		t.Fatalf("paths-only JSON must be a raw path array: %s", got)
	}
}
