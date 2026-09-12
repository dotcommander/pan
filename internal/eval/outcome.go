// Package eval evaluates explicit local review reports against explicit
// local outcome ledgers. It reads only the paths it is given, never contacts
// providers, and never inspects or mutates a target repository: the report
// documents already carry the deterministic evidence being judged.
package eval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/review"
)

// OutcomeSchema is stamped on every outcome ledger record.
const OutcomeSchema = "pan.outcome/v1"

const sourceOutcomeSchema = "slither.outcome/v1"

// maxOutcomeRecordLen bounds one outcome ledger line.
const maxOutcomeRecordLen = 1 << 20

// validVerdict reports whether v is one of the canonical verdicts.
func validVerdict(v string) bool {
	switch v {
	case VerdictConfirmed, VerdictRefuted, VerdictUnknown, VerdictSkipped:
		return true
	default:
		return false
	}
}

// OutcomeRecord is one privacy-minimal evaluation label: identities, rank,
// score, lane, verdict, and context cost. It deliberately contains no path,
// source, prompt, command, or free-form user input.
type OutcomeRecord struct {
	Schema      string `json:"schema"`
	Timestamp   string `json:"timestamp,omitempty"`
	ReportID    string `json:"report_id"`
	EvidenceID  string `json:"evidence_id"`
	Lane        string `json:"lane"`
	Rank        int    `json:"rank"`
	Score       int    `json:"score"`
	Verdict     string `json:"verdict"`
	FilesOpened int    `json:"files_opened"`
	ToolCalls   int    `json:"tool_calls"`
	ReviewMS    int    `json:"review_ms"`
}

// outcomeFieldNames returns the canonical ledger field set.
func outcomeFieldNames() []string {
	return []string{
		"schema", "report_id", "evidence_id", "lane", "rank", "score", "verdict",
		"files_opened", "tool_calls", "review_ms",
	}
}

func sourceOutcomeFieldNames() []string {
	return []string{
		"schema", "timestamp", "report_id", "evidence_id", "lane", "rank", "score", "verdict",
		"files_opened", "tool_calls", "review_ms",
	}
}

// decodeOutcomeRecord strictly decodes one ledger line: exactly the
// canonical field set, no unknown fields, no trailing data.
func decodeOutcomeRecord(raw []byte) (OutcomeRecord, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return OutcomeRecord{}, errors.New("record is not a JSON object")
	}
	var schema string
	if rawSchema, ok := fields["schema"]; !ok || json.Unmarshal(rawSchema, &schema) != nil {
		return OutcomeRecord{}, errors.New("record is missing schema")
	}
	fieldNames := outcomeFieldNames()
	if schema == sourceOutcomeSchema {
		fieldNames = sourceOutcomeFieldNames()
	}
	if len(fields) != len(fieldNames) {
		return OutcomeRecord{}, errors.New("record has unexpected fields")
	}
	for _, name := range fieldNames {
		if _, ok := fields[name]; !ok {
			return OutcomeRecord{}, fmt.Errorf("record is missing %s", name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record OutcomeRecord
	if err := decoder.Decode(&record); err != nil {
		return OutcomeRecord{}, fmt.Errorf("decode record: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return OutcomeRecord{}, errors.New("record has trailing data")
	}
	return record, nil
}

// validateOutcomeRecord enforces the ledger contract for decoded records.
func validateOutcomeRecord(record OutcomeRecord) error {
	if !validOutcomeFields(record) {
		return errors.New("invalid outcome record")
	}
	switch record.Schema {
	case OutcomeSchema:
		return validatePanOutcome(record)
	case sourceOutcomeSchema:
		return validateSourceOutcome(record)
	default:
		return errors.New("invalid outcome record")
	}

}

func validOutcomeFields(record OutcomeRecord) bool {
	return validVerdict(record.Verdict) && record.Rank > 0 && record.FilesOpened >= 0 && record.ToolCalls >= 0 && record.ReviewMS >= 0 && review.ValidIdentity(record.ReportID) && review.ValidIdentity(record.EvidenceID)
}

func validatePanOutcome(record OutcomeRecord) error {
	if record.Score < 0 || !review.ValidLane(record.Lane) {
		return errors.New("invalid outcome record")
	}
	return nil
}

func validateSourceOutcome(record OutcomeRecord) error {
	if record.Score < 1 || record.Score > 5 || !validSourceLane(record.Lane) {
		return errors.New("invalid outcome record")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.Timestamp); err != nil {
		return errors.New("invalid outcome timestamp")
	}
	return nil
}

// ledgerRead is one readOutcomeLine result.
type ledgerRead struct {
	line       []byte
	terminated bool
	oversized  bool
	eof        bool
	err        error
}

// streamOutcomeLedger streams the ledger line by line, applying each record.
// A final unterminated or truncated line is reported through
// partialTrailingRecords instead of failing the whole evaluation.
func streamOutcomeLedger(ctx context.Context, path string, partialTrailingRecords *int, apply func(OutcomeRecord) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open outcomes: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read := readOutcomeLine(reader)
		if errors.Is(read.err, io.EOF) && len(read.line) == 0 && !read.oversized {
			return nil
		}
		if read.err != nil && !errors.Is(read.err, io.EOF) {
			return fmt.Errorf("read outcome ledger: %w", read.err)
		}
		if stop, stopErr := processOutcomeLine(read, partialTrailingRecords, apply); stop {
			return stopErr
		}
	}
}

// processOutcomeLine applies one read ledger line. It reports whether
// streaming stops and, when it does, the stopping error.
func processOutcomeLine(read ledgerRead, partialTrailingRecords *int, apply func(OutcomeRecord) error) (bool, error) {
	if read.oversized {
		if read.eof && !read.terminated {
			*partialTrailingRecords++
			return true, nil
		}
		return true, errors.New("outcome record exceeds 1 MiB")
	}
	if len(bytes.TrimSpace(read.line)) == 0 {
		return read.eof, nil
	}
	if err := applyOutcomeLine(read.line, read.eof && !read.terminated, partialTrailingRecords, apply); err != nil {
		return true, err
	}
	return read.eof, nil
}

// applyOutcomeLine decodes, validates, and applies one non-empty ledger
// line. A truncated final line is tolerated as a partial record.
func applyOutcomeLine(line []byte, finalPartial bool, partialTrailingRecords *int, apply func(OutcomeRecord) error) error {
	record, decodeErr := decodeOutcomeRecord(line)
	if decodeErr != nil {
		if finalPartial && isTruncatedJSON(decodeErr) {
			*partialTrailingRecords++
			return nil
		}
		return fmt.Errorf("decode outcome ledger: %w", decodeErr)
	}
	if err := validateOutcomeRecord(record); err != nil {
		return err
	}
	return apply(record)
}

// isTruncatedJSON reports whether err looks like an unterminated final JSON
// value rather than malformed mid-stream data.
func isTruncatedJSON(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "unexpected end of JSON input")
}

// readOutcomeLine reads one newline-terminated ledger line, bounding its
// length. It mirrors bufio.ReadSlice semantics while tolerating CRLF.
func readOutcomeLine(reader *bufio.Reader) ledgerRead {
	var read ledgerRead
	for {
		fragment, readErr := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			read.terminated = fragment[len(fragment)-1] == '\n'
			fragment = trimLineTerminator(fragment, read.terminated)
			read.line, read.oversized = accumulateLine(read.line, read.oversized, fragment)
			if read.terminated {
				return read
			}
		}
		if readErr == nil || errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			read.eof = true
			return read
		}
		return ledgerRead{err: readErr}
	}
}

// trimLineTerminator strips one newline and any preceding carriage return
// from a terminated fragment.
func trimLineTerminator(fragment []byte, terminated bool) []byte {
	if !terminated {
		return fragment
	}
	fragment = fragment[:len(fragment)-1]
	if len(fragment) > 0 && fragment[len(fragment)-1] == '\r' {
		fragment = fragment[:len(fragment)-1]
	}
	return fragment
}

// accumulateLine appends one fragment while the line fits the ledger cap;
// once exceeded, the line keeps its bounded prefix and oversized stays
// latched.
func accumulateLine(line []byte, oversized bool, fragment []byte) ([]byte, bool) {
	if !oversized && len(line)+len(fragment) < maxOutcomeRecordLen {
		return append(line, fragment...), false
	}
	return line, true
}
