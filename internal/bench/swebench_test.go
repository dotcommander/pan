package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGoldPaths(t *testing.T) {
	t.Parallel()
	patch := `diff --git a/pkg/mod.go b/pkg/mod.go
--- a/pkg/mod.go
+++ b/pkg/mod.go
@@ -1 +1 @@
-old
+new
diff --git a/added.go b/added.go
--- /dev/null
+++ b/added.go
@@ -0,0 +1 @@
+new file
diff --git a/gone.go b/gone.go
--- a/gone.go
+++ /dev/null
@@ -1 +0 @@
-deleted
diff --git a/stamped.go b/stamped.go
--- a/stamped.go	2026-01-01 00:00:00.000000000 +0000
+++ b/stamped.go	2026-01-02 00:00:00.000000000 +0000
@@ -1 +1 @@
-old
+new
`
	got := GoldPaths(patch)
	want := []string{"added.go", "gone.go", "pkg/mod.go", "stamped.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("GoldPaths() = %v, want %v", got, want)
	}
	if paths := GoldPaths(""); len(paths) != 0 {
		t.Fatalf("GoldPaths(empty) = %v, want none", paths)
	}
	if paths := GoldPaths("no headers here"); len(paths) != 0 {
		t.Fatalf("GoldPaths(no headers) = %v, want none", paths)
	}
}

func TestLoadInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	instances := []map[string]any{
		{
			"instance_id":       "acme__widget-1",
			"repo":              "acme/widget",
			"base_commit":       "aaaa",
			"problem_statement": "Fix the widget parser",
			"patch":             "+++ b/widget.go",
		},
		{
			"instance_id":       "acme__gadget-2",
			"repo":              "acme/gadget",
			"base_commit":       "bbbb",
			"problem_statement": "Gadget crashes",
			"patch":             "+++ b/gadget.go",
		},
	}
	path := writeInstances(t, dir, instances)

	got, err := LoadInstances(path, nil, 0)
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d instances, want 2", len(got))
	}

	filtered, err := LoadInstances(path, []string{"acme/gadget"}, 0)
	if err != nil {
		t.Fatalf("LoadInstances filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "acme__gadget-2" {
		t.Fatalf("filtered = %+v, want only acme__gadget-2", filtered)
	}

	limited, err := LoadInstances(path, nil, 1)
	if err != nil {
		t.Fatalf("LoadInstances limited: %v", err)
	}
	if len(limited) != 1 || limited[0].ID != "acme__widget-1" {
		t.Fatalf("limited = %+v, want first instance", limited)
	}
}

func TestLoadInstancesRejectsInvalidRecords(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"missing-commit.jsonl": `{"instance_id":"x","repo":"a/b","problem_statement":"p","patch":"+++ b/x.go"}` + "\n",
		"bad-repo.jsonl":       `{"instance_id":"x","repo":"ab","base_commit":"c","problem_statement":"p","patch":"+++ b/x.go"}` + "\n",
		"unknown-field.jsonl":  `{"instance_id":"x","repo":"a/b","base_commit":"c","problem_statement":"p","patch":"+++ b/x.go","extra":1}` + "\n",
		"path-separator.jsonl": `{"instance_id":"a/b","repo":"a/b","base_commit":"c","problem_statement":"p","patch":"+++ b/x.go"}` + "\n",
		"no-match.jsonl":       `{"instance_id":"x","repo":"a/b","base_commit":"c","problem_statement":"p","patch":"+++ b/x.go"}` + "\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		filters := []string{}
		if name == "no-match.jsonl" {
			filters = []string{"z/z"}
		}
		if _, err := LoadInstances(path, filters, 0); err == nil {
			t.Fatalf("LoadInstances(%s) succeeded, want error", name)
		}
	}
}

func writeInstances(t *testing.T, dir string, instances []map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, "dataset.jsonl")
	var body []byte
	for _, instance := range instances {
		line, err := json.Marshal(instance)
		if err != nil {
			t.Fatal(err)
		}
		body = append(append(body, line...), '\n')
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMirrorPath(t *testing.T) {
	t.Parallel()
	got := MirrorPath("/mirrors", "acme/widget")
	want := filepath.Join("/mirrors", "acme__widget")
	if got != want {
		t.Fatalf("MirrorPath = %q, want %q", got, want)
	}
	if got := MirrorPath("/mirrors", "not-a-repo"); got != "" {
		t.Fatalf("MirrorPath(invalid) = %q, want empty", got)
	}
}

func TestBoundedRequest(t *testing.T) {
	t.Parallel()
	statement := "x"
	bounded, truncated := BoundedRequest(statement)
	if truncated || bounded != statement {
		t.Fatalf("BoundedRequest(short) = %q, truncated=%v", bounded, truncated)
	}
	long := make([]byte, maxRequestBytes+64)
	for i := range long {
		long[i] = 'a'
	}
	bounded, truncated = BoundedRequest(string(long))
	if !truncated {
		t.Fatal("BoundedRequest(long) reports no truncation")
	}
	if len(bounded) > maxRequestBytes {
		t.Fatalf("bounded length %d exceeds cap %d", len(bounded), maxRequestBytes)
	}
}
