package review

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// ExecutionReceiptSchema identifies the sealed host-agent execution receipt.
const ExecutionReceiptSchema = "pan.review-execution/v1"

// Terminal dispositions for one selected evidence row.
const (
	ExecutionReviewed = "reviewed"
	ExecutionSkipped  = "skipped"
)

// Derived receipt states. Complete means every selected row reached exactly
// one terminal disposition; partial means selected rows were omitted, which
// stays machine-visible instead of silently reading as complete.
const (
	ExecutionComplete = "complete"
	ExecutionPartial  = "partial"
)

// maxExecutionReasonLen bounds one disposition reason.
const maxExecutionReasonLen = 512

// ExecutionDisposition is one terminal disposition for one selected review
// row. Skipped rows require a bounded reason; reviewed rows may carry an
// optional bounded note.
type ExecutionDisposition struct {
	EvidenceID string `json:"evidence_id"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
}

// ExecutionSummary derives receipt state from the disposition partition. It
// is recomputed by Pan on every boundary; a submitted summary is validated,
// never trusted.
type ExecutionSummary struct {
	Selected int    `json:"selected"`
	Reviewed int    `json:"reviewed"`
	Skipped  int    `json:"skipped"`
	Missing  int    `json:"missing"`
	State    string `json:"state"`
}

// ExecutionReceipt is the versioned coverage receipt for one frozen
// selection document. Selected seals the evidence denominator at
// construction; Dispositions must reference it with at most one terminal
// disposition per row. Unknown, duplicate, or malformed rows fail
// validation; omitted rows surface as partial coverage in Summary.
type ExecutionReceipt struct {
	Schema       string                 `json:"schema"`
	SelectionID  string                 `json:"selection_id"`
	Selected     []string               `json:"selected"`
	Dispositions []ExecutionDisposition `json:"dispositions"`
	Summary      ExecutionSummary       `json:"summary"`
}

// NewExecutionReceipt seals the selected evidence denominator from doc and
// validates dispositions against it. The result is canonical: sorted
// denominator and dispositions, derived summary.
func NewExecutionReceipt(doc SelectionDocument, dispositions []ExecutionDisposition) (ExecutionReceipt, error) {
	if doc.Schema != SelectionDocumentSchema {
		return ExecutionReceipt{}, errors.New("execution receipt requires a selection document")
	}
	selected := selectedEvidenceIDs(doc)
	normalized, err := normalizeExecutionDispositions(selected, dispositions)
	if err != nil {
		return ExecutionReceipt{}, err
	}
	receipt := ExecutionReceipt{
		Schema:       ExecutionReceiptSchema,
		SelectionID:  doc.SelectionID,
		Selected:     selected,
		Dispositions: normalized,
	}
	receipt.Summary = executionSummary(receipt.Selected, receipt.Dispositions)
	return receipt, nil
}

// Bytes renders the receipt as compact deterministic JSON with one trailing
// newline.
func (r ExecutionReceipt) Bytes() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("review: encode execution receipt: %w", err)
	}
	return append(data, '\n'), nil
}

// ParseExecutionReceipt decodes and self-validates receipt bytes: canonical
// field sets, well-formed identities, at most one in-denominator disposition
// per row, and a summary that matches the recomputed partition. The sealed
// denominator makes the receipt self-contained; ValidateAgainstSelection
// then binds it to one selection document.
func ParseExecutionReceipt(data []byte) (ExecutionReceipt, error) {
	if len(data) > maxDocumentBytes {
		return ExecutionReceipt{}, fmt.Errorf("execution receipt exceeds %d bytes", maxDocumentBytes)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return ExecutionReceipt{}, errors.New("execution receipt is not a JSON object")
	}
	if err := requireJSONFields(fields, "schema", "selection_id", "selected", "dispositions", "summary"); err != nil {
		return ExecutionReceipt{}, fmt.Errorf("execution receipt: %w", err)
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(fields["summary"], &summary); err != nil || summary == nil {
		return ExecutionReceipt{}, errors.New("execution receipt summary is not an object")
	}
	if err := requireJSONFields(summary, "selected", "reviewed", "skipped", "missing", "state"); err != nil {
		return ExecutionReceipt{}, fmt.Errorf("execution receipt summary: %w", err)
	}
	receipt, err := decodeExecutionReceipt(data)
	if err != nil {
		return ExecutionReceipt{}, err
	}
	if err := validateParsedReceipt(receipt); err != nil {
		return ExecutionReceipt{}, err
	}
	return receipt, nil
}

// ValidateAgainstSelection binds a receipt to one selection document: the
// same selection identity, the same sealed selected set, canonical
// dispositions over that set, and a summary matching the recomputed
// partition. A receipt built against an older or different selection fails
// here rather than reading as complete.
func (r ExecutionReceipt) ValidateAgainstSelection(doc SelectionDocument) error {
	if doc.Schema != SelectionDocumentSchema {
		return errors.New("execution receipt requires a selection document")
	}
	if r.Schema != ExecutionReceiptSchema {
		return fmt.Errorf("unsupported execution receipt schema %q", r.Schema)
	}
	if r.SelectionID != doc.SelectionID {
		return fmt.Errorf("execution receipt selection %s does not match selection document %s", r.SelectionID, doc.SelectionID)
	}
	selected := selectedEvidenceIDs(doc)
	if !slices.Equal(r.Selected, selected) {
		return errors.New("execution receipt selected set does not match the selection document")
	}
	normalized, err := normalizeExecutionDispositions(selected, r.Dispositions)
	if err != nil {
		return err
	}
	if !slices.Equal(normalized, r.Dispositions) {
		return errors.New("execution receipt dispositions are not canonical")
	}
	if r.Summary != executionSummary(selected, normalized) {
		return errors.New("execution receipt summary does not match dispositions")
	}
	return nil
}

// ValidateExecutionReceipt binds one host-agent execution receipt to one
// selection document, both read from explicit local paths, and returns the
// canonical validated receipt. Reads are bounded by the document byte cap;
// validation failures name the responsible boundary.
func ValidateExecutionReceipt(selectionPath, receiptPath string) (ExecutionReceipt, error) {
	selectionBody, err := readBoundedDocument(selectionPath)
	if err != nil {
		return ExecutionReceipt{}, fmt.Errorf("read selection document: %w", err)
	}
	document, err := ParseSelectionDocument(selectionBody)
	if err != nil {
		return ExecutionReceipt{}, err
	}
	receiptBody, err := readBoundedDocument(receiptPath)
	if err != nil {
		return ExecutionReceipt{}, fmt.Errorf("read execution receipt: %w", err)
	}
	receipt, err := ParseExecutionReceipt(receiptBody)
	if err != nil {
		return ExecutionReceipt{}, err
	}
	if err := receipt.ValidateAgainstSelection(document); err != nil {
		return ExecutionReceipt{}, err
	}
	return receipt, nil
}

// readBoundedDocument reads at most maxDocumentBytes plus one byte from an
// explicit local path, so oversized input fails parsing instead of consuming
// unbounded memory.
func readBoundedDocument(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
}

// selectedEvidenceIDs returns the sorted evidence identities of every
// selected row in one selection document.
func selectedEvidenceIDs(doc SelectionDocument) []string {
	ids := make([]string, 0, len(doc.Items))
	for _, item := range doc.Items {
		if item.Decision == DecisionSelected {
			ids = append(ids, item.Input.EvidenceID)
		}
	}
	slices.Sort(ids)
	return ids
}

// normalizeExecutionDispositions validates and canonicalizes dispositions
// against the sealed denominator: well-formed identities, exactly one
// terminal disposition per referenced row, a bounded reason on every skipped
// row, and no row outside the denominator. The result is sorted by evidence
// id.
func normalizeExecutionDispositions(selected []string, dispositions []ExecutionDisposition) ([]ExecutionDisposition, error) {
	membership := make(map[string]bool, len(selected))
	for _, id := range selected {
		membership[id] = true
	}
	seen := make(map[string]bool, len(dispositions))
	normalized := make([]ExecutionDisposition, len(dispositions))
	for i, disposition := range dispositions {
		if !ValidIdentity(disposition.EvidenceID) {
			return nil, fmt.Errorf("execution receipt disposition %d has a malformed evidence identity", i+1)
		}
		if !membership[disposition.EvidenceID] {
			return nil, fmt.Errorf("execution receipt disposition %d references unknown evidence %s", i+1, disposition.EvidenceID)
		}
		if seen[disposition.EvidenceID] {
			return nil, fmt.Errorf("execution receipt has a duplicate disposition for evidence %s", disposition.EvidenceID)
		}
		seen[disposition.EvidenceID] = true
		switch disposition.State {
		case ExecutionReviewed:
		case ExecutionSkipped:
			if strings.TrimSpace(disposition.Reason) == "" {
				return nil, fmt.Errorf("execution receipt disposition %d skips evidence %s without a reason", i+1, disposition.EvidenceID)
			}
		default:
			return nil, fmt.Errorf("execution receipt disposition %d has invalid state %q", i+1, disposition.State)
		}
		if len(disposition.Reason) > maxExecutionReasonLen {
			return nil, fmt.Errorf("execution receipt disposition %d reason exceeds %d bytes", i+1, maxExecutionReasonLen)
		}
		normalized[i] = disposition
	}
	slices.SortFunc(normalized, func(a, b ExecutionDisposition) int {
		return cmp.Compare(a.EvidenceID, b.EvidenceID)
	})
	return normalized, nil
}

// executionSummary derives the terminal partition summary. Callers must
// already guarantee unique, in-denominator dispositions, so missing never
// goes negative.
func executionSummary(selected []string, dispositions []ExecutionDisposition) ExecutionSummary {
	summary := ExecutionSummary{Selected: len(selected)}
	covered := make(map[string]bool, len(dispositions))
	for _, disposition := range dispositions {
		covered[disposition.EvidenceID] = true
		switch disposition.State {
		case ExecutionReviewed:
			summary.Reviewed++
		case ExecutionSkipped:
			summary.Skipped++
		}
	}
	summary.Missing = summary.Selected - len(covered)
	if summary.Missing == 0 {
		summary.State = ExecutionComplete
	} else {
		summary.State = ExecutionPartial
	}
	return summary
}

func decodeExecutionReceipt(data []byte) (ExecutionReceipt, error) {
	var receipt ExecutionReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return ExecutionReceipt{}, fmt.Errorf("decode execution receipt: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ExecutionReceipt{}, errors.New("execution receipt has trailing data")
	}
	slices.Sort(receipt.Selected)
	dispositions, err := normalizeExecutionDispositions(receipt.Selected, receipt.Dispositions)
	if err != nil {
		return ExecutionReceipt{}, err
	}
	receipt.Dispositions = dispositions
	return receipt, nil
}

func validateParsedReceipt(receipt ExecutionReceipt) error {
	if receipt.Schema != ExecutionReceiptSchema {
		return fmt.Errorf("unsupported execution receipt schema %q", receipt.Schema)
	}
	if !ValidIdentity(receipt.SelectionID) {
		return errors.New("execution receipt has a malformed selection identity")
	}
	for i, id := range receipt.Selected {
		if !ValidIdentity(id) {
			return fmt.Errorf("execution receipt selected row %d has a malformed evidence identity", i+1)
		}
		if i > 0 && receipt.Selected[i-1] == id {
			return fmt.Errorf("execution receipt duplicates selected evidence %s", id)
		}
	}
	if receipt.Summary != executionSummary(receipt.Selected, receipt.Dispositions) {
		return errors.New("execution receipt summary does not match dispositions")
	}
	return nil
}

// RenderExecutionReceiptMarkdown renders one validated receipt as a compact
// Markdown coverage summary.
func RenderExecutionReceiptMarkdown(receipt ExecutionReceipt) string {
	var body strings.Builder
	body.WriteString("# Review execution receipt\n\n")
	fmt.Fprintf(&body, "- Selection: `%s`\n", receipt.SelectionID)
	fmt.Fprintf(&body, "- State: **%s**\n", receipt.Summary.State)
	fmt.Fprintf(&body, "- Selected: %d\n", receipt.Summary.Selected)
	fmt.Fprintf(&body, "- Reviewed: %d\n", receipt.Summary.Reviewed)
	fmt.Fprintf(&body, "- Skipped: %d\n", receipt.Summary.Skipped)
	fmt.Fprintf(&body, "- Missing: %d\n", receipt.Summary.Missing)
	if len(receipt.Dispositions) == 0 {
		return body.String()
	}
	body.WriteString("\n| Evidence | State | Reason |\n| --- | --- | --- |\n")
	for _, disposition := range receipt.Dispositions {
		fmt.Fprintf(&body, "| `%s` | %s | %s |\n", disposition.EvidenceID, disposition.State, disposition.Reason)
	}
	return body.String()
}
