package improve

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryLoadsSchemaLessJanitorJSONLAndRepairsMirror(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	want := legacyHistoryRecord()
	line, marshalErr := json.Marshal(legacyHistoryWire(want))
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	history := NewHistory(path)
	records, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyHistoryRecord(t, records, want)
	mirrored, err := history.loadSQLite()
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyHistoryRecord(t, mirrored, want)
}

func TestHistoryLoadsJanitorSQLiteWithoutJSONL(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	want := legacyHistoryRecord()
	seedJanitorSQLite(t, historyDBPath(path), want)

	records, err := NewHistory(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyHistoryRecord(t, records, want)
}

func TestHistoryAppendMigratesSQLiteOnlyLegacyLedger(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	legacy := legacyHistoryRecord()
	seedJanitorSQLite(t, historyDBPath(path), legacy)
	history := NewHistory(path)
	if err := history.Append(Record{RepoPath: legacy.RepoPath, RunType: RunTypeRefactor, Success: true, Outcome: string(OutcomeSuccess)}); err != nil {
		t.Fatal(err)
	}
	records, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %#v", records)
	}
	assertLegacyHistoryRecord(t, records[:1], legacy)
	if records[1].Schema != HistorySchema || records[1].RunType != RunTypeRefactor {
		t.Fatalf("appended record = %#v", records[1])
	}
}

func legacyHistoryRecord() Record {
	return Record{
		Timestamp:          time.Date(2026, 9, 12, 14, 30, 0, 0, time.UTC),
		StrategyID:         "janitor/strategy/v7",
		RunType:            RunTypePrep,
		RepoPath:           "/tmp/legacy-repo",
		Branch:             "main",
		Provider:           "local",
		Model:              "model-a",
		Success:            true,
		Reason:             "coverage raised",
		Outcome:            "coverage_increased",
		FeeEarned:          1.25,
		LinesDeleted:       9,
		LinesAdded:         3,
		NetReduction:       6,
		SymbolsDeleted:     2,
		SymbolsAdded:       1,
		SymbolsModified:    4,
		NetSymbolReduction: 1,
		StructuralMetrics:  true,
		DryRun:             true,
		Prep:               &PrepContext{CoverageIncreased: true, Targets: []PrepTargetContext{{RepoRelFile: "pkg/a.go", Outcome: "accepted"}}},
		Target:             &TargetContext{Source: "scan", RiskLanes: []string{"architecture"}},
		Refactor:           &RefactorContext{PackagePreflightMillis: 12, CandidateSource: "symbols"},
		Candidates:         &CandidatePacket{Files: []CandidateFile{{Path: "pkg/a.go", Lane: "surface", Actionability: "inspect"}}, SkippedSignals: []string{"history"}},
	}
}

func legacyHistoryWire(record Record) map[string]any {
	return map[string]any{
		"timestamp":            record.Timestamp,
		"strategy_id":          record.StrategyID,
		"run_type":             record.RunType,
		"repo_path":            record.RepoPath,
		"branch":               record.Branch,
		"provider":             record.Provider,
		"model":                record.Model,
		"success":              record.Success,
		"reason":               record.Reason,
		"outcome":              record.Outcome,
		"fee_earned":           record.FeeEarned,
		"lines_deleted":        record.LinesDeleted,
		"lines_added":          record.LinesAdded,
		"net_line_reduction":   record.NetReduction,
		"symbols_deleted":      record.SymbolsDeleted,
		"symbols_added":        record.SymbolsAdded,
		"symbols_modified":     record.SymbolsModified,
		"net_symbol_reduction": record.NetSymbolReduction,
		"structural_metrics":   record.StructuralMetrics,
		"dry_run":              record.DryRun,
		"prep":                 record.Prep,
		"target_context":       record.Target,
		"refactor_context":     record.Refactor,
		"candidate_packet":     record.Candidates,
	}
}

func seedJanitorSQLite(t *testing.T, path string, record Record) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		jsonl_index INTEGER NOT NULL UNIQUE,
		timestamp TEXT NOT NULL DEFAULT '', strategy_id TEXT NOT NULL DEFAULT '', run_type TEXT NOT NULL DEFAULT '',
		repo_path TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
		success INTEGER NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL DEFAULT '', fee_earned REAL NOT NULL DEFAULT 0,
		lines_deleted INTEGER NOT NULL DEFAULT 0, lines_added INTEGER NOT NULL DEFAULT 0, net_line_reduction INTEGER NOT NULL DEFAULT 0, dry_run INTEGER NOT NULL DEFAULT 0,
		symbols_deleted INTEGER NOT NULL DEFAULT 0, symbols_added INTEGER NOT NULL DEFAULT 0, symbols_modified INTEGER NOT NULL DEFAULT 0,
		net_symbol_reduction INTEGER NOT NULL DEFAULT 0, structural_metrics INTEGER NOT NULL DEFAULT 0,
		prep_context_json TEXT NOT NULL DEFAULT '', target_context_json TEXT NOT NULL DEFAULT '', refactor_context_json TEXT NOT NULL DEFAULT '', candidate_packet_json TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatal(err)
	}
	prep, err := json.Marshal(record.Prep)
	if err != nil {
		t.Fatal(err)
	}
	target, err := json.Marshal(record.Target)
	if err != nil {
		t.Fatal(err)
	}
	refactor, err := json.Marshal(record.Refactor)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := json.Marshal(record.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO runs(
		jsonl_index, timestamp, strategy_id, run_type, repo_path, branch, provider, model, success, reason, outcome, fee_earned,
		lines_deleted, lines_added, net_line_reduction, dry_run, symbols_deleted, symbols_added, symbols_modified, net_symbol_reduction,
		structural_metrics, prep_context_json, target_context_json, refactor_context_json, candidate_packet_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		0, record.Timestamp.Format(time.RFC3339Nano), record.StrategyID, record.RunType, record.RepoPath, record.Branch, record.Provider, record.Model,
		1, record.Reason, record.Outcome, record.FeeEarned, record.LinesDeleted, record.LinesAdded, record.NetReduction, 1,
		record.SymbolsDeleted, record.SymbolsAdded, record.SymbolsModified, record.NetSymbolReduction, 1, string(prep), string(target), string(refactor), string(candidates))
	if err != nil {
		t.Fatal(err)
	}
}

func assertLegacyHistoryRecord(t *testing.T, records []Record, want Record) {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	got := records[0]
	assertLegacyHistoryMetadata(t, got, want)
	assertLegacyHistoryContexts(t, got)
}

func assertLegacyHistoryMetadata(t *testing.T, got, want Record) {
	t.Helper()
	if got.Schema != "" || got.StrategyID != want.StrategyID || got.Provider != want.Provider || got.Model != want.Model || got.FeeEarned != want.FeeEarned || got.NetSymbolReduction != want.NetSymbolReduction || !got.StructuralMetrics {
		t.Fatalf("legacy metadata = %#v", got)
	}
}

func assertLegacyHistoryContexts(t *testing.T, got Record) {
	t.Helper()
	if got.Prep == nil || got.Target == nil || got.Refactor == nil || got.Candidates == nil {
		t.Fatalf("legacy contexts = %#v", got)
	}
	if got.Prep.Targets[0].Outcome != "accepted" || got.Target.RiskLanes[0] != "architecture" || got.Refactor.CandidateSource != "symbols" || got.Candidates.Files[0].Actionability != "inspect" {
		t.Fatalf("legacy contexts = %#v", got)
	}
}
