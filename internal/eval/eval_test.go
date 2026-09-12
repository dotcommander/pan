package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/review"
)

func TestEvaluateReportsScoreSaturationAndOutcomeTotals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	items := []review.ReadItem{
		{Rank: 1, Path: "a.go", Score: 10, Lane: review.LaneKept, Why: []string{"risk:security"}},
		{Rank: 2, Path: "b.go", Score: 10, Lane: review.LaneAlternate, Why: []string{"risk:api"}},
		{Rank: 3, Path: "c.go", Score: 5, Lane: review.LaneLowSignal, Why: []string{"churn:1"}},
	}
	for i := range items {
		items[i].EvidenceID = review.EvidenceIdentity(items[i])
	}
	doc := review.NewDocument(3, review.Report{ReadQueue: items})
	reportPath := filepath.Join(root, "report.json")
	data, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(reportPath, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	records := []OutcomeRecord{
		{Schema: OutcomeSchema, ReportID: doc.ReportID, EvidenceID: items[0].EvidenceID, Lane: items[0].Lane, Rank: 1, Score: 10, Verdict: VerdictConfirmed, FilesOpened: 1, ToolCalls: 2, ReviewMS: 3},
		{Schema: OutcomeSchema, ReportID: doc.ReportID, EvidenceID: items[1].EvidenceID, Lane: items[1].Lane, Rank: 2, Score: 10, Verdict: VerdictRefuted},
	}
	ledger := make([]byte, 0)
	for _, record := range records {
		line, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		ledger = append(append(ledger, line...), '\n')
	}
	outcomesPath := filepath.Join(root, "outcomes.jsonl")
	if writeErr := os.WriteFile(outcomesPath, ledger, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	got, err := Evaluate(context.Background(), outcomesPath, []string{reportPath})
	if err != nil {
		t.Fatal(err)
	}
	if got.Calibration.DistinctScoresInTopK != 2 || got.Calibration.TopKScoreSaturation != 2.0/3.0 {
		t.Fatalf("score calibration = %#v", got.Calibration)
	}
	if got.Outcomes.Confirmed != 1 || got.Outcomes.Refuted != 1 || got.ContextCost.ToolCalls != 2 {
		t.Fatalf("result = %#v", got)
	}
}

func TestEvaluateAcceptsValidatedSourceRunnableFixture(t *testing.T) {
	t.Parallel()
	fixture := filepath.Join("testdata", "slither_runnable_eval")
	got, err := Evaluate(t.Context(), filepath.Join(fixture, "outcomes.jsonl"), []string{filepath.Join(fixture, "report.json")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != Schema || got.Reports != 1 || got.Rows != 1 || got.Outcomes.Confirmed != 1 {
		t.Fatalf("result = %#v", got)
	}
	if got.ContextCost != (ContextCost{FilesOpened: 1, ToolCalls: 2, ReviewMS: 3}) {
		t.Fatalf("context cost = %#v", got.ContextCost)
	}
	if got.Calibration.Labeled != 1 || got.Calibration.Found != 1 || got.Calibration.ConfirmedInTopK != 1 || got.Calibration.DistinctScoresInTopK != 1 || got.Calibration.TopKSlots != 1 {
		t.Fatalf("calibration = %#v", got.Calibration)
	}
}

func TestEvaluateRejectsInvalidSourceProvenance(t *testing.T) {
	t.Parallel()
	fixture := filepath.Join("testdata", "slither_runnable_eval")
	report, err := os.ReadFile(filepath.Join(fixture, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	report = bytes.Replace(report, []byte(`"deterministic":4`), []byte(`"deterministic":3`), 1)
	root := t.TempDir()
	reportPath := filepath.Join(root, "report.json")
	if err := os.WriteFile(reportPath, report, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Evaluate(t.Context(), filepath.Join(fixture, "outcomes.jsonl"), []string{reportPath}); err == nil {
		t.Fatal("invalid source provenance accepted")
	}
}
