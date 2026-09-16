package review

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func receiptSelectionDocument(t *testing.T) SelectionDocument {
	t.Helper()
	status, report, options := selectionFixture()
	document, err := NewSelectionDocument(status, report, options)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func selectedReceiptEvidence(t *testing.T, document SelectionDocument) string {
	t.Helper()
	for _, item := range document.Items {
		if item.Decision == DecisionSelected {
			return item.Input.EvidenceID
		}
	}
	t.Fatal("fixture selection has no selected rows")
	return ""
}

func syntheticIdentity(seed byte) string {
	return "sha256:" + strings.Repeat(string(seed), 64)
}

func TestExecutionReceiptSealsDenominatorAndDerivesComplete(t *testing.T) {
	t.Parallel()
	document := receiptSelectionDocument(t)
	id := selectedReceiptEvidence(t, document)
	receipt, err := NewExecutionReceipt(document, []ExecutionDisposition{{EvidenceID: id, State: ExecutionReviewed}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != ExecutionReceiptSchema || receipt.SelectionID != document.SelectionID {
		t.Fatalf("receipt header = %s/%s", receipt.Schema, receipt.SelectionID)
	}
	if !reflect.DeepEqual(receipt.Selected, []string{id}) {
		t.Fatalf("sealed denominator = %#v", receipt.Selected)
	}
	want := ExecutionSummary{Selected: 1, Reviewed: 1, Missing: 0, State: ExecutionComplete}
	if receipt.Summary != want {
		t.Fatalf("summary = %#v, want %#v", receipt.Summary, want)
	}
	body, err := receipt.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(body), "}\n") || strings.HasSuffix(string(body), "\n\n") {
		t.Fatalf("receipt bytes must end with exactly one newline: %q", body)
	}
	parsed, err := ParseExecutionReceipt(body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, receipt) {
		t.Fatalf("round trip = %#v, want %#v", parsed, receipt)
	}
}

func TestExecutionReceiptDerivesPartialSkippedAndEmptyStates(t *testing.T) {
	t.Parallel()
	document := receiptSelectionDocument(t)
	id := selectedReceiptEvidence(t, document)

	partial, err := NewExecutionReceipt(document, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := ExecutionSummary{Selected: 1, Reviewed: 0, Skipped: 0, Missing: 1, State: ExecutionPartial}
	if partial.Summary != want {
		t.Fatalf("partial summary = %#v, want %#v", partial.Summary, want)
	}

	skipped, err := NewExecutionReceipt(document, []ExecutionDisposition{{
		EvidenceID: id,
		State:      ExecutionSkipped,
		Reason:     "generated vendor file excluded by policy",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want = ExecutionSummary{Selected: 1, Reviewed: 0, Skipped: 1, Missing: 0, State: ExecutionComplete}
	if skipped.Summary != want {
		t.Fatalf("skipped summary = %#v, want %#v", skipped.Summary, want)
	}

	empty, err := NewSelectionDocument(analyze.Status{}, Report{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	vacuous, err := NewExecutionReceipt(empty, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = ExecutionSummary{Selected: 0, State: ExecutionComplete}
	if vacuous.Summary != want {
		t.Fatalf("empty summary = %#v, want %#v", vacuous.Summary, want)
	}
}

func TestExecutionReceiptRejectsInvalidDispositions(t *testing.T) {
	t.Parallel()
	document := receiptSelectionDocument(t)
	id := selectedReceiptEvidence(t, document)
	overlong := strings.Repeat("x", maxExecutionReasonLen+1)
	for name, dispositions := range map[string][]ExecutionDisposition{
		"unknown evidence": {
			{EvidenceID: syntheticIdentity('0'), State: ExecutionReviewed},
		},
		"duplicate same state": {
			{EvidenceID: id, State: ExecutionReviewed},
			{EvidenceID: id, State: ExecutionReviewed},
		},
		"duplicate conflicting state": {
			{EvidenceID: id, State: ExecutionReviewed},
			{EvidenceID: id, State: ExecutionSkipped, Reason: "later decision"},
		},
		"skip without reason": {
			{EvidenceID: id, State: ExecutionSkipped},
		},
		"skip with blank reason": {
			{EvidenceID: id, State: ExecutionSkipped, Reason: "   "},
		},
		"reason over bound": {
			{EvidenceID: id, State: ExecutionSkipped, Reason: overlong},
		},
		"invalid state": {
			{EvidenceID: id, State: "deferred"},
		},
		"malformed identity": {
			{EvidenceID: "sha256:xyz", State: ExecutionReviewed},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewExecutionReceipt(document, dispositions); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

func TestExecutionReceiptRejectsNonSelectionDocument(t *testing.T) {
	t.Parallel()
	document := SelectionDocument{Schema: "pan.review-report/v1"}
	if _, err := NewExecutionReceipt(document, nil); err == nil {
		t.Fatal("non-selection document accepted")
	}
}

func TestParseExecutionReceiptRejectsInvalidContracts(t *testing.T) {
	t.Parallel()
	document := receiptSelectionDocument(t)
	id := selectedReceiptEvidence(t, document)
	receipt, err := NewExecutionReceipt(document, []ExecutionDisposition{{EvidenceID: id, State: ExecutionReviewed}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := receipt.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseExecutionReceipt(append(body, []byte("{}")...)); err == nil {
		t.Fatal("trailing data accepted")
	}
	for name, payload := range map[string][]byte{
		"not an object":          []byte("[]"),
		"not json":               []byte("receipt"),
		"missing summary":        []byte(`{"schema":"` + ExecutionReceiptSchema + `","selection_id":"` + receipt.SelectionID + `","selected":[],"dispositions":[]}`),
		"null dispositions":      []byte(`{"schema":"` + ExecutionReceiptSchema + `","selection_id":"` + receipt.SelectionID + `","selected":[],"dispositions":null,"summary":{"selected":0,"reviewed":0,"skipped":0,"missing":0,"state":"complete"}}`),
		"summary missing field":  []byte(`{"schema":"` + ExecutionReceiptSchema + `","selection_id":"` + receipt.SelectionID + `","selected":[],"dispositions":[],"summary":{"selected":0,"reviewed":0,"skipped":0,"missing":0}}`),
		"summary state mismatch": []byte(`{"schema":"` + ExecutionReceiptSchema + `","selection_id":"` + receipt.SelectionID + `","selected":["` + id + `"],"dispositions":[],"summary":{"selected":1,"reviewed":0,"skipped":0,"missing":1,"state":"complete"}}`),
		"unknown evidence":       []byte(`{"schema":"` + ExecutionReceiptSchema + `","selection_id":"` + receipt.SelectionID + `","selected":["` + id + `"],"dispositions":[{"evidence_id":"` + syntheticIdentity('9') + `","state":"reviewed"}],"summary":{"selected":1,"reviewed":1,"skipped":0,"missing":0,"state":"complete"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseExecutionReceipt(payload); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

func TestParseExecutionReceiptNormalizesSortedDenominator(t *testing.T) {
	t.Parallel()
	document := receiptSelectionDocument(t)
	id := selectedReceiptEvidence(t, document)
	other := syntheticIdentity('1')
	payload := `{"schema":"` + ExecutionReceiptSchema + `","selection_id":"` + document.SelectionID + `","selected":["` + other + `","` + id + `"],"dispositions":[{"evidence_id":"` + id + `","state":"reviewed"},{"evidence_id":"` + other + `","state":"skipped","reason":"duplicate lane"}],"summary":{"selected":2,"reviewed":1,"skipped":1,"missing":0,"state":"complete"}}`
	parsed, err := ParseExecutionReceipt([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	wantSelected := []string{other, id}
	if !reflect.DeepEqual(parsed.Selected, wantSelected) {
		t.Fatalf("selected = %#v, want %#v", parsed.Selected, wantSelected)
	}
	wantDispositions := []ExecutionDisposition{
		{EvidenceID: other, State: ExecutionSkipped, Reason: "duplicate lane"},
		{EvidenceID: id, State: ExecutionReviewed},
	}
	if !reflect.DeepEqual(parsed.Dispositions, wantDispositions) {
		t.Fatalf("dispositions = %#v, want %#v", parsed.Dispositions, wantDispositions)
	}
}

func TestValidateAgainstSelectionRejectsMismatch(t *testing.T) {
	t.Parallel()
	document := receiptSelectionDocument(t)
	id := selectedReceiptEvidence(t, document)
	receipt, err := NewExecutionReceipt(document, []ExecutionDisposition{{EvidenceID: id, State: ExecutionReviewed}})
	if err != nil {
		t.Fatal(err)
	}

	stale := receipt
	stale.SelectionID = syntheticIdentity('a')
	if err := stale.ValidateAgainstSelection(document); err == nil {
		t.Fatal("stale selection identity accepted")
	} else if !strings.Contains(err.Error(), "does not match selection document") {
		t.Fatalf("stale error = %v", err)
	}

	wrongDenominator := receipt
	wrongDenominator.Selected = []string{syntheticIdentity('b')}
	if err := wrongDenominator.ValidateAgainstSelection(document); err == nil {
		t.Fatal("wrong denominator accepted")
	}

	summaryDrift := receipt
	summaryDrift.Summary.Reviewed = 0
	if err := summaryDrift.ValidateAgainstSelection(document); err == nil {
		t.Fatal("summary drift accepted")
	}

	nonCanonical := receipt
	nonCanonical.Dispositions = nil
	if err := nonCanonical.ValidateAgainstSelection(document); err == nil {
		t.Fatal("non-canonical dispositions accepted")
	}

	if err := receipt.ValidateAgainstSelection(SelectionDocument{Schema: "other"}); err == nil {
		t.Fatal("non-selection document accepted")
	}
	if err := receipt.ValidateAgainstSelection(document); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
}

func TestNewExecutionReceiptSortsDispositions(t *testing.T) {
	t.Parallel()
	first := syntheticIdentity('1')
	second := syntheticIdentity('2')
	document := SelectionDocument{
		Schema: SelectionDocumentSchema,
		Items: []SelectionItem{
			{Input: ReadItem{Rank: 1, EvidenceID: second}, Decision: DecisionSelected},
			{Input: ReadItem{Rank: 2, EvidenceID: first}, Decision: DecisionSelected},
		},
	}
	receipt, err := NewExecutionReceipt(document, []ExecutionDisposition{
		{EvidenceID: second, State: ExecutionSkipped, Reason: "out of scope"},
		{EvidenceID: first, State: ExecutionReviewed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt.Selected, []string{first, second}) {
		t.Fatalf("selected = %#v", receipt.Selected)
	}
	want := []ExecutionDisposition{
		{EvidenceID: first, State: ExecutionReviewed},
		{EvidenceID: second, State: ExecutionSkipped, Reason: "out of scope"},
	}
	if !reflect.DeepEqual(receipt.Dispositions, want) {
		t.Fatalf("dispositions = %#v, want %#v", receipt.Dispositions, want)
	}
	if receipt.Summary.State != ExecutionComplete || receipt.Summary.Selected != 2 {
		t.Fatalf("summary = %#v", receipt.Summary)
	}
}

func TestRenderExecutionReceiptMarkdown(t *testing.T) {
	t.Parallel()
	document := receiptSelectionDocument(t)
	id := selectedReceiptEvidence(t, document)
	receipt, err := NewExecutionReceipt(document, []ExecutionDisposition{{
		EvidenceID: id,
		State:      ExecutionSkipped,
		Reason:     "generated migration file",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := RenderExecutionReceiptMarkdown(receipt)
	for _, want := range []string{
		"# Review execution receipt",
		"**" + ExecutionComplete + "**",
		"Selected: 1",
		"Skipped: 1",
		"generated migration file",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("markdown missing %q:\n%s", want, rendered)
		}
	}
	empty := RenderExecutionReceiptMarkdown(ExecutionReceipt{Summary: ExecutionSummary{State: ExecutionPartial}})
	if strings.Contains(empty, "| Evidence |") {
		t.Fatalf("empty receipt rendered a disposition table:\n%s", empty)
	}
}
