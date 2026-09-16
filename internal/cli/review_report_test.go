package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/review"
)

type failingWriter struct{ err error }

func (w failingWriter) Write(p []byte) (int, error) { return 0, w.err }

func TestReviewReportCmdValidateAcceptsLocalParityControls(t *testing.T) {
	t.Parallel()
	command := cli.ReviewReportCmd{
		Top:       25,
		MaxBytes:  1 << 20,
		Days:      30,
		Focus:     "auth|token",
		Include:   []string{"internal/**"},
		Exclude:   []string{"**/*_test.go"},
		Inventory: "data-integrity",
		WhyTop:    3,
		Output:    "-",
	}
	if err := command.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestReviewReportCmdHelpListsPreview(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := cli.Run(context.Background(), []string{"review", "report", "--help"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--preview") {
		t.Fatalf("help missing --preview:\n%s", out.String())
	}
}

func TestReviewReportCmdPreviewEmitsOnlySelectionDocument(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := cli.Run(t.Context(), []string{"--repo", basicGoRepo(), "review", "report", "--preview"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	body := out.Bytes()
	if !bytes.HasSuffix(body, []byte{'\n'}) || bytes.HasSuffix(body, []byte("\n\n")) {
		t.Fatalf("document must end with exactly one newline: %q", body[len(body)-min(3, len(body)):])
	}
	document, err := review.ParseSelectionDocument(body)
	if err != nil {
		t.Fatalf("parse preview: %v\n%s", err, body)
	}
	if document.Schema != review.SelectionDocumentSchema {
		t.Fatalf("schema = %q", document.Schema)
	}
	if document.Summary.Candidates == 0 {
		t.Fatalf("empty population: %#v", document.Summary)
	}
}

func TestReviewReportCmdPreviewRejectsConflictingFlagsBeforeAcquisition(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	conflicting := [][]string{
		{"--model", "test-model"},
		{"--local"},
		{"--base-url", "http://127.0.0.1:9"},
		{"--api-key-env", "PAN_TEST_API_KEY"},
		{"--no-cache"},
		{"--cache-dir", t.TempDir()},
		{"--markdown"},
		{"--json"},
		{"--summary"},
		{"--cull"},
		{"--output", filepath.Join(t.TempDir(), "report.json")},
	}
	for _, flag := range conflicting {
		name := strings.TrimLeft(flag[0], "-")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"--repo", missing, "review", "report", "--preview"}, flag...)
			var out strings.Builder
			err := cli.Run(context.Background(), args, newTestDeps(&out))
			if err == nil {
				t.Fatalf("--preview with %s accepted; output=%s", flag[0], out.String())
			}
			if !strings.Contains(err.Error(), "--preview cannot be combined") {
				t.Fatalf("error = %v, want pre-acquisition preview conflict", err)
			}
		})
	}
}

func TestReviewReportCmdPreviewRejectsArtifactBeforeAppWork(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	artifact := filepath.Join(t.TempDir(), "artifact.json")
	var out strings.Builder
	err := cli.Run(context.Background(), []string{"--repo", missing, "--artifact", artifact, "review", "report", "--preview"}, newTestDeps(&out))
	if err == nil {
		t.Fatalf("preview with --artifact accepted; output=%s artifact=%s", out.String(), artifact)
	}
	if !strings.Contains(err.Error(), "--artifact") {
		t.Fatalf("error = %v, want artifact rejection", err)
	}
	if _, err := os.Stat(artifact); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact created: %v", err)
	}
}

func TestReviewReportCmdPreviewPropagatesWriterFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("writer closed")
	deps := newTestDeps(failingWriter{err: wantErr})
	err := cli.Run(t.Context(), []string{"--repo", basicGoRepo(), "review", "report", "--preview"}, deps)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want writer failure %v", err, wantErr)
	}
}

func TestReviewReportCmdValidateRejectsInvalidInventory(t *testing.T) {
	t.Parallel()
	command := cli.ReviewReportCmd{Top: 25, MaxBytes: 1 << 20, Days: 30, Inventory: "unknown", Output: "-"}
	if err := command.Validate(); err == nil {
		t.Fatal("invalid inventory accepted")
	}
}

func TestReviewReportCmdWritesDefaultDocumentToOutAlias(t *testing.T) {
	t.Parallel()
	repo := basicGoRepo()
	output := t.TempDir() + "/report.json"
	if err := cli.Run(t.Context(), []string{"--repo", repo, "review", "report", "--out", output}, newTestDeps(&strings.Builder{})); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"schema":"pan.review-report/v1"`)) {
		t.Fatalf("default report document = %s", body)
	}
}

func TestReviewReportCmdJSONPreservesCullLedger(t *testing.T) {
	t.Parallel()
	repo := basicGoRepo()
	output := t.TempDir() + "/report.json"
	if err := cli.Run(t.Context(), []string{"--repo", repo, "review", "report", "--cull", "--json", "--out", output}, newTestDeps(&strings.Builder{})); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := review.ParseDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.CullLedger == nil || doc.CullLedger.Schema != review.CullLedgerSchema {
		t.Fatalf("cull ledger = %#v", doc.CullLedger)
	}
}
