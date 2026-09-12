package improve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExportObservationsPreservesRecordedOutcomeWithoutRelabeling(t *testing.T) {
	dir := t.TempDir()
	history := HistoryPath(dir)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	record := Record{Schema: HistorySchema, Timestamp: when, RunType: "refactor", RepoPath: "/repo", RepoHead: "abc", Success: true, Outcome: "success", Reason: "checks passed", DryRun: true, ChangedFiles: []string{"a.go"}, Provider: "external", Model: "model"}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(history, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	observations, err := ExportObservations(history, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].Schema != RefactorObservationSchema || observations[0].Record.Success != true || observations[0].Record.Outcome != "success" {
		t.Fatalf("observations=%+v", observations)
	}
	output := filepath.Join(dir, "export.jsonl")
	if err := WriteObservations(output, observations); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("empty export")
	}
}

func TestExportObservationsRejectsHardLimit(t *testing.T) {
	_, err := ExportObservations("unused", MaxObservationExport+1, nil)
	if err == nil {
		t.Fatal("expected hard-limit error")
	}
}
