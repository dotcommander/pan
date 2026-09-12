package cli_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/review"
)

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
