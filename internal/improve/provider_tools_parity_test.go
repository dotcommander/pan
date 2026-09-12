package improve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProviderReaderJanitorExplorationTools(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/tools\n\ngo 1.25\n")
	writeFile(t, root, "src/a.go", "package src\n\nfunc Alpha() {}\nfunc Needle() {}\n")
	writeFile(t, root, "src/nested/b.go", "package nested\n\nfunc Beta() {}\n")
	writeFile(t, root, ".env", "TOP_SECRET=never\n")
	reader, err := newProviderReader(root, nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	checks := []struct {
		name, arguments, want string
	}{
		{providerReadFile, `{"path":"src/a.go","start_line":3,"end_line":3}`, "func Alpha"},
		{providerMultiRead, `{"files":[{"path":"src/a.go","start_line":4,"end_line":4},{"path":"src/nested/b.go"}]}`, "func Needle"},
		{providerListDir, `{"path":"src","depth":1}`, "src/a.go"},
		{providerSearchFiles, `{"path":"src","pattern":"Needle","include":"*.go"}`, "src/a.go:4"},
		{providerFindFiles, `{"path":"src","pattern":"**/*.go"}`, "src/nested/b.go"},
		{providerStatFile, `{"path":"src/a.go"}`, `"path":"src/a.go"`},
		{providerDetectProject, `{}`, `"go.mod"`},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()
			got, err := reader.call(ctx, check.name, check.arguments)
			if err != nil || !strings.Contains(got, check.want) {
				t.Fatalf("%s = %q, %v; want %q", check.name, got, err, check.want)
			}
		})
	}
	if _, err := reader.call(ctx, providerReadFile, `{"path":".env"}`); err == nil {
		t.Fatal("secret path was readable")
	}
}

func TestProviderReaderNativeDiagnosticsUsesGoplsCheck(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "source.go", "package source\n")
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "gopls")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' \"$2:3:5: fake diagnostic\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	reader, err := newProviderReader(root, []string{"not-present.go"}, 512)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.call(context.Background(), providerLSPQuery, `{"action":"diagnostics","path":"source.go"}`)
	if err != nil || !strings.Contains(result, "fake diagnostic") || !strings.Contains(result, `"capability":"diagnostics"`) {
		t.Fatalf("native diagnostics = %q, %v", result, err)
	}
	if _, err := reader.call(context.Background(), providerLSPQuery, `{"action":"diagnostics","path":".env"}`); err == nil {
		t.Fatal("diagnostics read excluded path")
	}
}

func TestProviderReaderNativeDiagnosticsReportsCommandFailure(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "source.go", "package source\n")
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "gopls")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' 'failed diagnostic' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	reader, err := newProviderReader(root, []string{"not-present.go"}, 512)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.call(context.Background(), providerLSPQuery, `{"action":"diagnostics","path":"source.go"}`)
	if err != nil || !strings.Contains(result, `"status":"unavailable"`) || !strings.Contains(result, `"reason":"query_failed"`) || !strings.Contains(result, "failed diagnostic") {
		t.Fatalf("failed diagnostics = %q, %v", result, err)
	}
}

func TestProviderReaderReadFileIsBounded(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "large.txt", strings.Repeat("x", providerMaxReadBytes+1))
	reader, err := newProviderReader(root, nil, providerMaxReadBytes+128)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.call(context.Background(), providerReadFile, `{"path":"large.txt"}`)
	if err != nil || !strings.Contains(result, "[source read truncated]") || len(result) > providerMaxReadBytes+128 {
		t.Fatalf("bounded read = %d bytes, %v", len(result), err)
	}
}

func TestProviderReaderNativeContextAndLSPUnavailable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/context\n\ngo 1.25\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	reader, err := newProviderReader(root, nil, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mapResult, err := reader.call(context.Background(), providerRepoContext, `{"action":"map","tokens":512,"max_bytes":1000}`)
	if err != nil || !strings.Contains(mapResult, `"mode"`) {
		t.Fatalf("repo_context map = %q, %v", mapResult, err)
	}
	lspResult, err := reader.call(context.Background(), providerLSPQuery, `{"action":"symbols","path":"main.go"}`)
	if err != nil || !strings.Contains(lspResult, `"status"`) {
		t.Fatalf("lsp_query = %q, %v", lspResult, err)
	}
}

func TestProviderReaderJinnCancellationAndPathGuard(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "source.go", "package source\n")
	reader, err := newProviderReader(root, nil, 512)
	if err != nil {
		t.Fatal(err)
	}
	reader.jinnBin = "sh"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = reader.call(ctx, providerReadFile, `{"path":"source.go"}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("jinn cancellation error = %v", err)
	}
	_, err = reader.call(context.Background(), providerMultiRead, `{"files":[{"path":"../secret"}]}`)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("jinn nested path guard error = %v", err)
	}
}

func TestProviderReaderJinnDiagnosticResponse(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "source.go", "package source\n")
	bin := filepath.Join(t.TempDir(), "jinn")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' '{\"ok\":true,\"result\":\"diagnostics ready\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	reader, err := newProviderReader(root, nil, 512)
	if err != nil {
		t.Fatal(err)
	}
	reader.jinnBin = bin
	result, err := reader.call(context.Background(), providerLSPQuery, `{"action":"diagnostics","path":"source.go"}`)
	if err != nil || result != "diagnostics ready" {
		t.Fatalf("diagnostics result = %q, %v", result, err)
	}
}

func TestProviderReaderJinnCancellationWinsOverResponse(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "source.go", "package source\n")
	bin := filepath.Join(t.TempDir(), "jinn")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' '{\"ok\":true,\"result\":\"too late\"}'\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	reader, err := newProviderReader(root, nil, 512)
	if err != nil {
		t.Fatal(err)
	}
	reader.jinnBin = bin
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := reader.call(ctx, providerReadFile, `{"path":"source.go"}`)
	if !errors.Is(err, context.DeadlineExceeded) || result != "" {
		t.Fatalf("cancelled jinn result = %q, %v", result, err)
	}
}

func TestFilterJinnResultDropsDeniedPaths(t *testing.T) {
	t.Parallel()
	result := filterJinnResult(providerSearchFiles, ".env:1:TOP_SECRET\nsrc/a.go:2:ok\n")
	if strings.Contains(result, "TOP_SECRET") || !strings.Contains(result, "src/a.go") {
		t.Fatalf("filtered result = %q", result)
	}
}
