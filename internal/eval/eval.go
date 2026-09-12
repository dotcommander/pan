package eval

import (
	"context"
	"errors"
	"fmt"
)

// Schema is stamped on every evaluation result document.
const Schema = "pan.eval/v1"

// TopK is the fixed recall window every evaluation reports against.
const TopK = 15

// Canonical outcome verdicts.
const (
	VerdictConfirmed = "confirmed"
	VerdictRefuted   = "refuted"
	VerdictUnknown   = "unknown"
	VerdictSkipped   = "skipped"
)

// Result is the deterministic evaluation document: aggregate counts only,
// with no paths, identities, or input filenames.
type Result struct {
	Schema      string      `json:"schema"`
	Reports     int         `json:"reports"`
	Rows        int         `json:"rows"`
	Outcomes    Outcomes    `json:"outcomes"`
	ContextCost ContextCost `json:"context_cost"`
	Calibration Calibration `json:"calibration"`
	Warnings    Warnings    `json:"warnings"`
}

// Outcomes counts verdicts over matched records.
type Outcomes struct {
	Confirmed int `json:"confirmed"`
	Refuted   int `json:"refuted"`
	Unknown   int `json:"unknown"`
	Skipped   int `json:"skipped"`
}

// ContextCost accumulates the review effort recorded per matched outcome.
type ContextCost struct {
	FilesOpened int `json:"files_opened"`
	ToolCalls   int `json:"tool_calls"`
	ReviewMS    int `json:"review_ms"`
}

// Calibration reports how well the ranked evidence predicted the labeled
// outcomes, judged against the fixed TopK window.
type Calibration struct {
	Labeled              int     `json:"labeled"`
	Found                int     `json:"found"`
	Missing              int     `json:"missing"`
	TopK                 int     `json:"top_k"`
	NoiseInTopK          int     `json:"noise_in_top_k"`
	ConfirmedInTopK      int     `json:"confirmed_in_top_k"`
	DistinctScoresInTopK int     `json:"distinct_scores_in_top_k"`
	TotalRows            int     `json:"total_rows"`
	TopKSlots            int     `json:"top_k_slots"`
	NoiseTopKRate        float64 `json:"noise_top_k_rate"`
	ConfirmedTopKRecall  float64 `json:"confirmed_top_k_recall"`
	ConfirmedFoundRecall float64 `json:"confirmed_found_recall"`
	LabeledCoverage      float64 `json:"labeled_coverage"`
	TopKScoreSaturation  float64 `json:"top_k_score_saturation"`
}

// Warnings counts ledger irregularities that did not fail the evaluation.
type Warnings struct {
	PartialTrailingRecords int `json:"partial_trailing_records"`
	UnmatchedOutcomes      int `json:"unmatched_outcomes"`
}

// calibrationTotals accumulates the confirmed-outcome totals the recall
// ratios share.
type calibrationTotals struct {
	confirmedFound   int
	missingConfirmed int
}

// Evaluate reads the explicit report documents and outcome ledger, applies
// every outcome to its report row, and returns the aggregate result. Records
// that reference unknown reports or evidence become unmatched warnings; a
// record that references known evidence with stale rank, score, or lane is
// an integrity failure.
func Evaluate(ctx context.Context, outcomesPath string, reportPaths []string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	reports := make(map[string]evalReport, len(reportPaths))
	result := Result{Schema: Schema, Reports: len(reportPaths), Calibration: Calibration{TopK: TopK}}
	var totals calibrationTotals
	for _, path := range reportPaths {
		doc, err := readEvalReport(path)
		if err != nil {
			return Result{}, err
		}
		if _, exists := reports[doc.ID]; exists {
			return Result{}, fmt.Errorf("duplicate report identity %s", doc.ID)
		}
		reports[doc.ID] = doc
		result.Rows += len(doc.Rows)
		result.Calibration.TotalRows += len(doc.Rows)
		result.Calibration.TopKSlots += min(TopK, len(doc.Rows))
		result.Calibration.DistinctScoresInTopK += distinctTopKScores(doc.Rows)
	}
	partialTrailingRecords := 0
	if err := streamOutcomeLedger(ctx, outcomesPath, &partialTrailingRecords, func(record OutcomeRecord) error {
		return applyOutcomeRecord(&result, &totals, reports, record)
	}); err != nil {
		return Result{}, err
	}
	result.Warnings.PartialTrailingRecords = partialTrailingRecords
	confirmedTotal := totals.confirmedFound + totals.missingConfirmed
	result.Calibration.NoiseTopKRate = ratio(result.Calibration.NoiseInTopK, result.Calibration.TopKSlots)
	result.Calibration.ConfirmedTopKRecall = ratio(result.Calibration.ConfirmedInTopK, confirmedTotal)
	result.Calibration.ConfirmedFoundRecall = ratio(totals.confirmedFound, confirmedTotal)
	result.Calibration.LabeledCoverage = ratio(result.Calibration.Found, result.Calibration.Labeled)
	result.Calibration.TopKScoreSaturation = ratio(result.Calibration.DistinctScoresInTopK, result.Calibration.TopKSlots)
	return result, nil
}

func distinctTopKScores(items []evalRow) int {
	seen := make(map[int]struct{}, min(TopK, len(items)))
	for _, item := range items[:min(TopK, len(items))] {
		seen[item.Score] = struct{}{}
	}
	return len(seen)
}

// applyOutcomeRecord folds one ledger record into the result.
func applyOutcomeRecord(result *Result, totals *calibrationTotals, reports map[string]evalReport, record OutcomeRecord) error {
	doc, ok := reports[record.ReportID]
	if !ok {
		return addUnmatchedOutcome(result, totals, record)
	}
	if doc.OutcomeSchema != record.Schema {
		return errors.New("outcome schema does not match report schema")
	}
	var row *evalRow
	for i := range doc.Rows {
		if doc.Rows[i].EvidenceID == record.EvidenceID {
			row = &doc.Rows[i]
			break
		}
	}
	if row == nil {
		return addUnmatchedOutcome(result, totals, record)
	}
	if record.Rank != row.Rank || record.Score != row.Score || record.Lane != row.Lane {
		return errors.New("outcome integrity mismatch")
	}
	switch record.Verdict {
	case VerdictConfirmed:
		result.Outcomes.Confirmed++
		result.Calibration.Labeled++
		result.Calibration.Found++
		totals.confirmedFound++
		if record.Rank <= TopK {
			result.Calibration.ConfirmedInTopK++
		}
	case VerdictRefuted:
		result.Outcomes.Refuted++
		result.Calibration.Labeled++
		result.Calibration.Found++
		if record.Rank <= TopK {
			result.Calibration.NoiseInTopK++
		}
	case VerdictUnknown:
		result.Outcomes.Unknown++
	case VerdictSkipped:
		result.Outcomes.Skipped++
	}
	result.ContextCost.FilesOpened += record.FilesOpened
	result.ContextCost.ToolCalls += record.ToolCalls
	result.ContextCost.ReviewMS += record.ReviewMS
	return nil
}

// addUnmatchedOutcome records a ledger record that references evidence no
// report provided. Confirmed and refuted verdicts still count toward the
// labeled and missing totals they earned.
func addUnmatchedOutcome(result *Result, totals *calibrationTotals, record OutcomeRecord) error {
	result.Warnings.UnmatchedOutcomes++
	if record.Verdict == VerdictConfirmed || record.Verdict == VerdictRefuted {
		result.Calibration.Labeled++
		result.Calibration.Missing++
		if record.Verdict == VerdictConfirmed {
			totals.missingConfirmed++
		}
	}
	return nil
}

// ratio computes numerator/denominator, defining 0/0 as 0 so empty ledgers
// and empty reports stay deterministic.
func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
