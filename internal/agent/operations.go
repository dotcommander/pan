package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/dotcommander/pan/internal/eval"
	"github.com/dotcommander/pan/internal/review"
)

const (
	evidenceIDField  = "evidence_id"
	filesOpenedField = "files_opened"
	reviewMSField    = "review_ms"
	verdictField     = "verdict"
	toolCallsField   = "tool_calls"
)

type rowSelection struct {
	ids   map[string]bool
	focus string
	limit int
}

func selectRows(doc review.Document, fields map[string]json.RawMessage) ([]review.ReadItem, error) {
	selection, err := parseRowSelection(doc.ReadQueue, fields)
	if err != nil {
		return nil, err
	}
	result := make([]review.ReadItem, 0, selection.limit)
	for _, row := range doc.ReadQueue {
		if rowSelected(row, selection) && len(result) < selection.limit {
			result = append(result, row)
		}
	}
	return result, nil
}

func parseRowSelection(queue []review.ReadItem, fields map[string]json.RawMessage) (rowSelection, error) {
	ids, err := selectionIDs(fields)
	if err != nil {
		return rowSelection{}, err
	}
	focus := ""
	if focusErr := decodeString(fields, "focus", &focus, false); focusErr != nil {
		return rowSelection{}, focusErr
	}
	limit, err := selectionLimit(queue, fields)
	if err != nil {
		return rowSelection{}, err
	}
	if err := requireKnownIDs(queue, ids); err != nil {
		return rowSelection{}, err
	}
	return rowSelection{ids: ids, focus: strings.ToLower(focus), limit: limit}, nil
}

func selectionIDs(fields map[string]json.RawMessage) (map[string]bool, error) {
	var values []string
	if raw, ok := fields["target_ids"]; ok && (bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &values) != nil) {
		return nil, errors.New("invalid target ids")
	}
	ids := make(map[string]bool, len(values))
	for _, id := range values {
		if id == "" {
			return nil, errTargetNotFound
		}
		ids[id] = true
	}
	return ids, nil
}

func selectionLimit(queue []review.ReadItem, fields map[string]json.RawMessage) (int, error) {
	limit := len(queue)
	raw, ok := fields["limit"]
	if !ok {
		return limit, nil
	}
	if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &limit) != nil || limit < 1 || limit > 100 {
		return 0, errors.New("invalid limit")
	}
	return limit, nil
}

func requireKnownIDs(queue []review.ReadItem, ids map[string]bool) error {
	for id := range ids {
		found := false
		for _, row := range queue {
			if row.EvidenceID == id {
				found = true
				break
			}
		}
		if !found {
			return errTargetNotFound
		}
	}
	return nil
}

func rowSelected(row review.ReadItem, selection rowSelection) bool {
	if len(selection.ids) == 0 && selection.focus == "" {
		return true
	}
	if selection.ids[row.EvidenceID] {
		return true
	}
	return selection.focus != "" && strings.Contains(strings.ToLower(row.Path+" "+strings.Join(row.Why, " ")), selection.focus)
}

func contextBudget(fields map[string]json.RawMessage) (int, error) {
	raw, ok := fields["budget_bytes"]
	if !ok || bytes.Equal(raw, []byte("null")) {
		return 0, errors.New("invalid context budget")
	}
	var budget int
	if err := json.Unmarshal(raw, &budget); err != nil {
		return 0, errors.New("invalid context budget")
	}
	if budget < 1 {
		return 0, ErrContextBudgetTooSmall
	}
	if budget > MaxRequestBytes {
		return 0, ErrContextBudgetTooLarge
	}
	return budget, nil
}

func feedbackRecord(doc review.Document, fields map[string]json.RawMessage) (eval.OutcomeRecord, error) {
	var reportID, evidenceID, verdict string
	for name, destination := range map[string]*string{reportIDField: &reportID, evidenceIDField: &evidenceID, verdictField: &verdict} {
		if err := decodeString(fields, name, destination, true); err != nil {
			return eval.OutcomeRecord{}, err
		}
	}
	if verdict != eval.VerdictConfirmed && verdict != eval.VerdictRefuted && verdict != eval.VerdictUnknown && verdict != eval.VerdictSkipped {
		return eval.OutcomeRecord{}, errors.New("invalid verdict")
	}
	if reportID != doc.ReportID {
		return eval.OutcomeRecord{}, errTargetNotFound
	}
	record := eval.OutcomeRecord{Schema: eval.OutcomeSchema, ReportID: reportID, EvidenceID: evidenceID, Verdict: verdict}
	for name, destination := range map[string]*int{filesOpenedField: &record.FilesOpened, toolCallsField: &record.ToolCalls, reviewMSField: &record.ReviewMS} {
		raw, ok := fields[name]
		if !ok || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, destination) != nil || *destination < 0 {
			return eval.OutcomeRecord{}, errors.New("invalid feedback cost")
		}
	}
	for _, row := range doc.ReadQueue {
		if row.EvidenceID == evidenceID {
			record.Rank, record.Score, record.Lane = row.Rank, row.Score, row.Lane
			return record, nil
		}
	}
	return eval.OutcomeRecord{}, errTargetNotFound
}
