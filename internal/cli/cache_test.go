package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

// runCache executes one cache subcommand against basicGoRepo with an
// isolated cache directory and returns its output.
func runCache(t *testing.T, dir, sub string) string {
	t.Helper()
	var out bytes.Buffer
	args := []string{"--repo", basicGoRepo(), "--format", "json", "cache", sub, "--cache-dir", dir}
	if err := cli.Run(context.Background(), args, newTestDeps(&out)); err != nil {
		t.Fatalf("cache %s: %v\noutput:\n%s", sub, err, out.String())
	}
	return out.String()
}

func TestCacheLifecycleStatusWarmClear(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if got := runCache(t, dir, "status"); !bytes.Contains([]byte(got), []byte(`"exists": false`)) ||
		!bytes.Contains([]byte(got), []byte(`"reason": "missing_cache"`)) {
		t.Fatalf("initial status must report a missing cache:\n%s", got)
	}

	if got := runCache(t, dir, "warm"); !bytes.Contains([]byte(got), []byte(`"usable": true`)) ||
		!bytes.Contains([]byte(got), []byte(`"stale": false`)) {
		t.Fatalf("warm must leave a fresh usable cache:\n%s", got)
	}

	if got := runCache(t, dir, "status"); !bytes.Contains([]byte(got), []byte(`"usable": true`)) ||
		!bytes.Contains([]byte(got), []byte(`"stale": false`)) {
		t.Fatalf("post-warm status must be fresh:\n%s", got)
	}

	if got := runCache(t, dir, "clear"); !bytes.Contains([]byte(got), []byte(`"removed"`)) {
		t.Fatalf("clear must report removed entries:\n%s", got)
	}

	if got := runCache(t, dir, "status"); !bytes.Contains([]byte(got), []byte(`"exists": false`)) {
		t.Fatalf("post-clear status must report a missing cache:\n%s", got)
	}
}
