// Document is the versioned, machine-readable projection of a review
// report. `review report --json` emits it verbatim, the agent report
// operation returns it, and `review eval` consumes it as the report input.
package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// DocumentSchema is the schema stamped on every review report document.
const DocumentSchema = "pan.review-report/v1"

// Document is the deterministic report contract: content identities for
// every row plus the parameters that shaped the queue. ReportID is derived
// from the rows, so identical evidence always yields an identical document.
type Document struct {
	Schema     string      `json:"schema"`
	ReportID   string      `json:"report_id"`
	Top        int         `json:"top"`
	Days       int         `json:"days"`
	ReadQueue  []ReadItem  `json:"read_queue"`
	CullLedger *CullLedger `json:"cull_ledger,omitempty"`
}

// NewDocument projects a composed report into the versioned document,
// deriving every row identity and the report identity.
func NewDocument(top int, report Report) Document {
	doc := Document{
		Schema:     DocumentSchema,
		Top:        top,
		Days:       report.Changes.Days,
		ReadQueue:  report.ReadQueue,
		CullLedger: report.CullLedger,
	}
	if doc.ReadQueue == nil {
		doc.ReadQueue = []ReadItem{}
	}
	doc.ReportID = reportIdentity(doc)
	return doc
}

// Bytes renders the document as compact deterministic JSON with one trailing
// newline, suitable for hashing, fixtures, and piping between commands.
func (d Document) Bytes() ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("review: encode document: %w", err)
	}
	return append(data, '\n'), nil
}

// ReadDocumentFile reads and validates a report document from path.
func ReadDocumentFile(path string) (Document, error) {
	file, err := os.Open(path)
	if err != nil {
		return Document{}, fmt.Errorf("review: open report %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		return Document{}, fmt.Errorf("review: read report %s: %w", path, err)
	}
	if len(data) > maxDocumentBytes {
		return Document{}, fmt.Errorf("review: report %s exceeds %d bytes", path, maxDocumentBytes)
	}
	doc, err := ParseDocument(data)
	if err != nil {
		return Document{}, fmt.Errorf("review: invalid report %s: %w", path, err)
	}
	return doc, nil
}

// maxDocumentBytes bounds one report document read.
const maxDocumentBytes = 4 << 20

// ParseDocument decodes and validates report document bytes. Validation is
// strict: exactly one JSON object, no unknown fields, a canonical schema,
// well-formed identities, unique evidence rows in rank order with canonical
// lanes, and a report identity that re-derives from the decoded rows.
func ParseDocument(data []byte) (Document, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return Document{}, errors.New("document is not a JSON object")
	}
	if err := requireDocumentFields(fields); err != nil {
		return Document{}, err
	}
	doc, err := decodeDocument(data)
	if err != nil {
		return Document{}, err
	}
	if doc.ReadQueue == nil {
		doc.ReadQueue = []ReadItem{}
	}
	if err := validateDocumentShape(doc); err != nil {
		return Document{}, err
	}
	if err := validateReadQueue(doc); err != nil {
		return Document{}, err
	}
	if reportIdentity(doc) != doc.ReportID {
		return Document{}, errors.New("report identity mismatch")
	}
	return doc, nil
}

// requireDocumentFields rejects a decoded JSON object that is missing any
// document field or carries a null read queue.
func requireDocumentFields(fields map[string]json.RawMessage) error {
	for _, name := range []string{"schema", "report_id", "top", "days", "read_queue"} {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("document is missing %s", name)
		}
	}
	if bytes.Equal(fields["read_queue"], []byte("null")) {
		return errors.New("read_queue must be an array")
	}
	return nil
}

// decodeDocument strictly decodes exactly one document object, rejecting
// unknown fields and trailing data.
func decodeDocument(data []byte) (Document, error) {
	var doc Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("decode document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Document{}, errors.New("document has trailing data")
	}
	return doc, nil
}

// validateDocumentShape checks the schema, identities, and non-negative
// parameters of one decoded document.
func validateDocumentShape(doc Document) error {
	if doc.Schema != DocumentSchema {
		return fmt.Errorf("unsupported schema %q", doc.Schema)
	}
	if !ValidIdentity(doc.ReportID) {
		return errors.New("malformed report identity")
	}
	if doc.Top < 0 || doc.Days < 0 {
		return errors.New("top and days must not be negative")
	}
	return nil
}

// validateReadQueue checks every row's rank, identities, uniqueness,
// score, and lane; the queue is already normalized to non-nil.
func validateReadQueue(doc Document) error {
	seen := make(map[string]bool, len(doc.ReadQueue))
	for i, item := range doc.ReadQueue {
		if err := validateReadItem(item, i, seen); err != nil {
			return err
		}
	}
	return nil
}

// validateReadItem checks one read-queue row at its zero-based index,
// recording its evidence identity in seen.
func validateReadItem(item ReadItem, index int, seen map[string]bool) error {
	if item.Rank != index+1 {
		return fmt.Errorf("row %d has rank %d", index+1, item.Rank)
	}
	if !ValidIdentity(item.EvidenceID) {
		return fmt.Errorf("row %d has a malformed evidence identity", index+1)
	}
	if seen[item.EvidenceID] {
		return fmt.Errorf("row %d duplicates evidence identity", index+1)
	}
	seen[item.EvidenceID] = true
	if item.Score < 0 {
		return fmt.Errorf("row %d has a negative score", index+1)
	}
	if !ValidLane(item.Lane) {
		return fmt.Errorf("row %d has non-canonical lane %q", index+1, item.Lane)
	}
	return nil
}
