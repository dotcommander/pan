package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/review"
)

func previewSelectionDocument(t *testing.T) (review.SelectionDocument, string) {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Run(t.Context(), []string{"--repo", basicGoRepo(), "review", "report", "--preview"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	document, err := review.ParseSelectionDocument(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "selection.json")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return document, path
}

func selectedReceiptIDs(t *testing.T, document review.SelectionDocument) []string {
	t.Helper()
	var ids []string
	for _, item := range document.Items {
		if item.Decision == review.DecisionSelected {
			ids = append(ids, item.Input.EvidenceID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("preview selection has no selected rows")
	}
	return ids
}

func reviewedDispositions(ids []string) []review.ExecutionDisposition {
	dispositions := make([]review.ExecutionDisposition, 0, len(ids))
	for _, id := range ids {
		dispositions = append(dispositions, review.ExecutionDisposition{EvidenceID: id, State: review.ExecutionReviewed})
	}
	return dispositions
}

func writeReceiptFile(t *testing.T, receipt review.ExecutionReceipt) string {
	t.Helper()
	body, err := receipt.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReviewCmdHelpListsReceipt(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := cli.Run(t.Context(), []string{"review", "--help"}, newTestDeps(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "receipt") {
		t.Fatalf("help missing receipt subcommand:\n%s", out.String())
	}
}

func TestReviewReceiptValidatesCompleteReceipt(t *testing.T) {
	t.Parallel()
	document, selectionPath := previewSelectionDocument(t)
	ids := selectedReceiptIDs(t, document)
	receipt, err := review.NewExecutionReceipt(document, reviewedDispositions(ids))
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := writeReceiptFile(t, receipt)
	var out bytes.Buffer
	err = cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--json",
	}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	validated, err := review.ParseExecutionReceipt(out.Bytes())
	if err != nil {
		t.Fatalf("parse validated receipt: %v\n%s", err, out.String())
	}
	if validated.Summary.State != review.ExecutionComplete || validated.SelectionID != document.SelectionID {
		t.Fatalf("validated receipt = %#v", validated.Summary)
	}
}

func TestReviewReceiptReportsPartialCoverage(t *testing.T) {
	t.Parallel()
	document, selectionPath := previewSelectionDocument(t)
	ids := selectedReceiptIDs(t, document)
	receipt, err := review.NewExecutionReceipt(document, reviewedDispositions(ids[:len(ids)-1]))
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := writeReceiptFile(t, receipt)
	var out bytes.Buffer
	err = cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--json",
	}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	validated, err := review.ParseExecutionReceipt(out.Bytes())
	if err != nil {
		t.Fatalf("parse validated receipt: %v\n%s", err, out.String())
	}
	if validated.Summary.State != review.ExecutionPartial || validated.Summary.Missing != 1 {
		t.Fatalf("partial coverage not machine-visible: %#v", validated.Summary)
	}
}

func TestReviewReceiptEmitsMarkdownSummary(t *testing.T) {
	t.Parallel()
	document, selectionPath := previewSelectionDocument(t)
	ids := selectedReceiptIDs(t, document)
	receipt, err := review.NewExecutionReceipt(document, reviewedDispositions(ids))
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := writeReceiptFile(t, receipt)
	var out bytes.Buffer
	err = cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--markdown",
	}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "State: **"+review.ExecutionComplete+"**") {
		t.Fatalf("markdown missing state:\n%s", out.String())
	}
}

func TestReviewReceiptWritesOutputFile(t *testing.T) {
	t.Parallel()
	document, selectionPath := previewSelectionDocument(t)
	ids := selectedReceiptIDs(t, document)
	receipt, err := review.NewExecutionReceipt(document, reviewedDispositions(ids))
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := writeReceiptFile(t, receipt)
	outputPath := filepath.Join(t.TempDir(), "validated.json")
	var out bytes.Buffer
	err = cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--json",
		"--output", outputPath,
	}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("output file mode wrote to stdout: %q", out.String())
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := review.ParseExecutionReceipt(body)
	if err != nil {
		t.Fatalf("parse written receipt: %v", err)
	}
	if validated.Summary.State != review.ExecutionComplete {
		t.Fatalf("written receipt state = %q", validated.Summary.State)
	}
}

func TestReviewReceiptRejectsStaleSelection(t *testing.T) {
	t.Parallel()
	document, selectionPath := previewSelectionDocument(t)
	ids := selectedReceiptIDs(t, document)
	receipt, err := review.NewExecutionReceipt(document, reviewedDispositions(ids))
	if err != nil {
		t.Fatal(err)
	}
	stale := receipt
	stale.SelectionID = "sha256:" + strings.Repeat("a", 64)
	receiptPath := writeReceiptFile(t, stale)
	var out bytes.Buffer
	err = cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--json",
	}, newTestDeps(&out))
	if err == nil {
		t.Fatalf("stale receipt accepted; output=%s", out.String())
	}
	if !strings.Contains(err.Error(), "does not match selection document") {
		t.Fatalf("error = %v, want stale selection rejection", err)
	}
}

func TestReviewReceiptRejectsUnknownEvidence(t *testing.T) {
	t.Parallel()
	document, selectionPath := previewSelectionDocument(t)
	ids := selectedReceiptIDs(t, document)
	unknown := "sha256:" + strings.Repeat("9", 64)
	payload := `{"schema":"` + review.ExecutionReceiptSchema + `","selection_id":"` + document.SelectionID + `","selected":[` +
		quotedIDs(ids) + `],"dispositions":[{"evidence_id":"` + unknown + `","state":"reviewed"}],"summary":{"selected":` +
		strconv.Itoa(len(ids)) + `,"reviewed":1,"skipped":0,"missing":0,"state":"complete"}}`
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(receiptPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--json",
	}, newTestDeps(&out))
	if err == nil {
		t.Fatalf("unknown evidence accepted; output=%s", out.String())
	}
	if !strings.Contains(err.Error(), "unknown evidence") {
		t.Fatalf("error = %v, want unknown evidence rejection", err)
	}
}

func TestReviewReceiptRejectsMalformedReceipt(t *testing.T) {
	t.Parallel()
	_, selectionPath := previewSelectionDocument(t)
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(receiptPath, []byte("not a receipt"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--json",
	}, newTestDeps(&out))
	if err == nil {
		t.Fatalf("malformed receipt accepted; output=%s", out.String())
	}
}

func TestReviewReceiptRejectsOutputAlias(t *testing.T) {
	t.Parallel()
	document, selectionPath := previewSelectionDocument(t)
	ids := selectedReceiptIDs(t, document)
	receipt, err := review.NewExecutionReceipt(document, reviewedDispositions(ids))
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := writeReceiptFile(t, receipt)
	var out bytes.Buffer
	err = cli.Run(t.Context(), []string{
		"--repo", basicGoRepo(), "review", "receipt",
		"--selection", selectionPath, "--receipt", receiptPath, "--json",
		"--output", selectionPath,
	}, newTestDeps(&out))
	if err == nil {
		t.Fatalf("output alias accepted; output=%s", out.String())
	}
	if !strings.Contains(err.Error(), "receipt output aliases an input") {
		t.Fatalf("error = %v, want output alias rejection", err)
	}
}

func TestReviewReceiptValidateRequiresOneFormat(t *testing.T) {
	t.Parallel()
	neither := cli.ReviewReceiptCmd{Selection: "s", Receipt: "r", Output: "-"}
	if err := neither.Validate(); err == nil {
		t.Fatal("neither format accepted")
	}
	both := cli.ReviewReceiptCmd{Selection: "s", Receipt: "r", Output: "-", JSON: true, Markdown: true}
	if err := both.Validate(); err == nil {
		t.Fatal("both formats accepted")
	}
	emptyOutput := cli.ReviewReceiptCmd{Selection: "s", Receipt: "r", JSON: true}
	if err := emptyOutput.Validate(); err == nil {
		t.Fatal("empty output accepted")
	}
}

// quotedIDs renders evidence ids as a JSON string array body.
func quotedIDs(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, `"`+id+`"`)
	}
	return strings.Join(parts, ",")
}
