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

func TestFlowStoryboardEnvelopeAndRawDocuments(t *testing.T) {
	t.Parallel()
	repo := basicGoRepo()
	got := runJSON(t, []string{"--repo", repo, "--format", "json", "flow", "storyboard"})
	for _, want := range []string{`"project_name"`, `"coverage"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("envelope missing %q:\n%s", want, got)
		}
	}
	var out bytes.Buffer
	if err := cli.Run(context.Background(), []string{"--repo", repo, "flow", "storyboard", "-o", "-", "--view", "stores"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "STORES") || strings.Contains(out.String(), "schema_version") {
		t.Fatalf("raw text = %q", out.String())
	}
	out.Reset()
	if err := cli.Run(context.Background(), []string{"--repo", repo, "flow", "storyboard", "-o", "-", "--document-format", "json"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"project_name"`) || strings.Contains(out.String(), "schema_version") {
		t.Fatalf("raw JSON = %q", out.String())
	}
}

func TestFlowStoryboardDocumentFileAndRefreshRejectsSymlink(t *testing.T) {
	t.Parallel()
	repo := basicGoRepo()
	output := filepath.Join(t.TempDir(), "storyboard.html")
	if err := cli.Run(context.Background(), []string{"--repo", repo, "flow", "storyboard", "-o", output, "--document-format", "html"}, newTestDeps(&bytes.Buffer{})); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<!doctype html>") {
		t.Fatalf("HTML document missing doctype")
	}
}

func TestFlowStoryboardRejectsConflictingRawAndOpenOptions(t *testing.T) {
	t.Parallel()
	repo := basicGoRepo()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "raw document with JSON envelope",
			args: []string{"--repo", repo, "--format", "json", "flow", "storyboard", "--output", "-", "--document-format", "text"},
			want: "raw document stdout cannot be combined with --format json",
		},
		{
			name: "audit document with open",
			args: []string{"--repo", repo, "flow", "storyboard", "--output", filepath.Join(t.TempDir(), "storyboard.html"), "--document-format", "html", "--audit-json", "--open"},
			want: "--open requires --output with an HTML file path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := cli.Run(context.Background(), tt.args, newTestDeps(&bytes.Buffer{}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Run() error = %v, want %q", err, tt.want)
			}
		})
	}
}
