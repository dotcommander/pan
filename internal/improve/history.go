package improve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// HistorySchema stamps every local improve history record.
const (
	HistorySchema     = "pan.improve-history/v1"
	sqliteEmptyString = "''"
	sqliteProvider    = "provider"
)

// Record is one append-only local history entry for a guarded improve run.
// Only refactor and prep workflows append records; read-only commands never
// write history.
type Record struct {
	StrategyID         string           `json:"strategy_id,omitempty"`
	Branch             string           `json:"branch,omitempty"`
	Provider           string           `json:"provider,omitempty"`
	Model              string           `json:"model,omitempty"`
	FeeEarned          float64          `json:"fee_earned,omitempty"`
	NetSymbolReduction int              `json:"net_symbol_reduction,omitempty"`
	SymbolsAdded       int              `json:"symbols_added,omitempty"`
	SymbolsModified    int              `json:"symbols_modified,omitempty"`
	StructuralMetrics  bool             `json:"structural_metrics,omitempty"`
	Prep               *PrepContext     `json:"prep,omitempty"`
	Refactor           *RefactorContext `json:"refactor_context,omitempty"`
	Target             *TargetContext   `json:"target_context,omitempty"`
	Candidates         *CandidatePacket `json:"candidate_packet,omitempty"`
	Schema             string           `json:"schema"`
	Timestamp          time.Time        `json:"timestamp"`
	RunType            string           `json:"run_type"`
	RepoPath           string           `json:"repo_path"`
	RepoHead           string           `json:"repo_head,omitempty"`
	Success            bool             `json:"success"`
	Outcome            string           `json:"outcome"`
	Reason             string           `json:"reason,omitempty"`
	DryRun             bool             `json:"dry_run"`
	LinesAdded         int              `json:"lines_added,omitempty"`
	LinesDeleted       int              `json:"lines_deleted,omitempty"`
	NetReduction       int              `json:"net_line_reduction,omitempty"`
	SymbolsDeleted     int              `json:"symbols_deleted,omitempty"`
	ChangedFiles       []string         `json:"changed_files,omitempty"`
	BaselineCoverage   float64          `json:"baseline_coverage,omitempty"`
	FinalCoverage      float64          `json:"final_coverage,omitempty"`
	AlreadySufficient  bool             `json:"already_sufficient,omitempty"`
}

// History keeps JSONL as the append-compatible authority and mirrors it in
// SQLite for durable aggregate queries. Load repairs the mirror from JSONL.
type History struct{ path string }

// NewHistory returns a ledger rooted at path.
func NewHistory(path string) *History { return &History{path: path} }

// HistoryPath returns the ledger file path inside a state directory.
func HistoryPath(stateDir string) string { return filepath.Join(stateDir, "history.jsonl") }

// Path reports the ledger file path.
func (h *History) Path() string { return h.path }

// Append writes rec as one JSON line after stamping the schema.
func (h *History) Append(rec Record) error {
	rec.Schema = HistorySchema
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	if err := h.establishJSONLAuthority(); err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode history record: %w", err)
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(h.path), 0o750); mkdirErr != nil {
		return fmt.Errorf("create history directory: %w", mkdirErr)
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, writeErr := f.Write(append(line, '\n')); writeErr != nil {
		return fmt.Errorf("write history: %w", writeErr)
	}
	if syncErr := f.Sync(); syncErr != nil {
		return fmt.Errorf("sync history: %w", syncErr)
	}
	db, err := h.openDB(context.Background())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if syncErr := h.syncDB(context.Background(), db); syncErr != nil {
		return syncErr
	}
	return nil
}

// establishJSONLAuthority imports a SQLite-only legacy ledger before the
// first append. Once JSONL exists it remains the sole history authority.
func (h *History) establishJSONLAuthority() error {
	if _, statErr := os.Stat(h.path); statErr == nil {
		return nil
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat history: %w", statErr)
	}
	legacy, err := h.loadSQLite()
	if err != nil {
		return err
	}
	if len(legacy) == 0 {
		return nil
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(h.path), 0o750); mkdirErr != nil {
		return fmt.Errorf("create history directory: %w", mkdirErr)
	}
	temp, err := os.CreateTemp(filepath.Dir(h.path), ".history-migrate-*")
	if err != nil {
		return fmt.Errorf("create migrated history: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set migrated history permissions: %w", err)
	}
	for _, record := range legacy {
		line, err := json.Marshal(record)
		if err != nil {
			_ = temp.Close()
			return fmt.Errorf("encode migrated history: %w", err)
		}
		if _, err := temp.Write(append(line, '\n')); err != nil {
			_ = temp.Close()
			return fmt.Errorf("write migrated history: %w", err)
		}
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync migrated history: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close migrated history: %w", err)
	}
	if err := os.Link(tempPath, h.path); os.IsExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("publish migrated history: %w", err)
	}
	return nil
}

// Load returns every ledger record, oldest first. A missing ledger is an
// empty history, not an error; a malformed line is an error so silent
// ledger corruption can never masquerade as zero runs.
func (h *History) Load() ([]Record, error) {
	if _, err := os.Stat(h.path); os.IsNotExist(err) {
		return h.loadSQLite()
	} else if err != nil {
		return nil, fmt.Errorf("stat history: %w", err)
	}
	records, err := h.loadJSONL()
	if err != nil {
		return records, err
	}
	db, err := h.openDB(context.Background())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if err := h.syncDB(context.Background(), db); err != nil {
		return nil, err
	}
	return records, nil
}

// loadSQLite supports the Pan improvement SQLite-only migration path. JSONL remains
// authoritative whenever it exists; this path is used only when the JSONL
// ledger is absent and a prior SQLite ledger is all that remains.
func (h *History) loadSQLite() ([]Record, error) {
	if _, err := os.Stat(historyDBPath(h.path)); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat history sqlite: %w", err)
	}
	db, err := h.openDB(context.Background())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	records, hasPayload, err := loadPayloadRecords(context.Background(), db)
	if err != nil {
		return nil, err
	}
	if hasPayload {
		return records, nil
	}
	return loadLegacySQLiteRecords(context.Background(), db)
}

func (h *History) loadJSONL() ([]Record, error) {
	data, err := os.ReadFile(h.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	var records []Record
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse history line %d: %w", i+1, err)
		}
		if rec.Schema != "" && rec.Schema != HistorySchema {
			return nil, fmt.Errorf("parse history line %d: unsupported schema %q", i+1, rec.Schema)
		}
		records = append(records, rec)
	}
	return records, nil
}

func historyDBPath(jsonlPath string) string {
	return strings.TrimSuffix(jsonlPath, ".jsonl") + ".sqlite"
}

func (h *History) openDB(ctx context.Context) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(h.path), 0o750); err != nil {
		return nil, fmt.Errorf("create history directory: %w", err)
	}
	db, err := sql.Open("sqlite", historyDBPath(h.path))
	if err != nil {
		return nil, fmt.Errorf("open history sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA synchronous=NORMAL", "PRAGMA journal_mode=WAL", `CREATE TABLE IF NOT EXISTS runs (jsonl_index INTEGER PRIMARY KEY, payload TEXT NOT NULL)`} {
		if _, execErr := db.ExecContext(ctx, stmt); execErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize history sqlite: %w", execErr)
		}
	}
	if err := ensurePayloadColumn(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ensurePayloadColumn(ctx context.Context, db *sql.DB) error {
	columns, err := historyTableColumns(ctx, db)
	if err != nil {
		return err
	}
	if columns["payload"] {
		return nil
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE runs ADD COLUMN payload TEXT"); err != nil {
		return fmt.Errorf("add history payload column: %w", err)
	}
	return nil
}

func historyTableColumns(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(runs)")
	if err != nil {
		return nil, fmt.Errorf("inspect history sqlite schema: %w", err)
	}
	defer func() { _ = rows.Close() }()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("inspect history sqlite column: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect history sqlite schema: %w", err)
	}
	return columns, nil
}

func loadPayloadRecords(ctx context.Context, db *sql.DB) ([]Record, bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT payload FROM runs ORDER BY jsonl_index")
	if err != nil {
		return nil, false, fmt.Errorf("read history sqlite: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []Record
	hasPayload := false
	hasEmptyPayload := false
	for rows.Next() {
		var payload sql.NullString
		if err := rows.Scan(&payload); err != nil {
			return nil, false, fmt.Errorf("scan history sqlite: %w", err)
		}
		if strings.TrimSpace(payload.String) == "" {
			hasEmptyPayload = true
			continue
		}
		hasPayload = true
		var rec Record
		if err := json.Unmarshal([]byte(payload.String), &rec); err != nil {
			return nil, false, fmt.Errorf("parse history sqlite: %w", err)
		}
		if rec.Schema != "" && rec.Schema != HistorySchema {
			return nil, false, fmt.Errorf("parse history sqlite: unsupported schema %q", rec.Schema)
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read history sqlite: %w", err)
	}
	if hasPayload && hasEmptyPayload {
		return nil, false, errors.New("read history sqlite: mixed legacy and payload rows")
	}
	return records, hasPayload, nil
}

func loadLegacySQLiteRecords(ctx context.Context, db *sql.DB) ([]Record, error) {
	columns, err := historyTableColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	if !columns["jsonl_index"] {
		return nil, errors.New("read history sqlite: missing jsonl_index column")
	}
	fields := []struct {
		name     string
		fallback string
	}{
		{"timestamp", sqliteEmptyString}, {"strategy_id", sqliteEmptyString}, {"run_type", sqliteEmptyString}, {"repo_path", sqliteEmptyString},
		{"branch", sqliteEmptyString}, {sqliteProvider, sqliteEmptyString}, {"model", sqliteEmptyString}, {string(OutcomeSuccess), "0"},
		{"reason", sqliteEmptyString}, {"outcome", sqliteEmptyString}, {"fee_earned", "0"}, {"lines_deleted", "0"},
		{"lines_added", "0"}, {"net_line_reduction", "0"}, {"dry_run", "0"}, {"symbols_deleted", "0"},
		{"symbols_added", "0"}, {"symbols_modified", "0"}, {"net_symbol_reduction", "0"},
		{"structural_metrics", "0"}, {"prep_context_json", sqliteEmptyString}, {"target_context_json", sqliteEmptyString},
		{"refactor_context_json", sqliteEmptyString}, {"candidate_packet_json", sqliteEmptyString},
	}
	selects := make([]string, 0, len(fields))
	for _, field := range fields {
		selects = append(selects, sqliteColumnOrFallback(columns, field.name, field.fallback))
	}
	rows, err := db.QueryContext(ctx, legacySQLiteQuery(selects))
	if err != nil {
		return nil, fmt.Errorf("read legacy history sqlite: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []Record
	for rows.Next() {
		var (
			record                                        Record
			timestamp, prep, target, refactor, candidates string
			success, dryRun, structuralMetrics            int
		)
		if err := rows.Scan(
			&timestamp, &record.StrategyID, &record.RunType, &record.RepoPath,
			&record.Branch, &record.Provider, &record.Model, &success,
			&record.Reason, &record.Outcome, &record.FeeEarned, &record.LinesDeleted,
			&record.LinesAdded, &record.NetReduction, &dryRun, &record.SymbolsDeleted,
			&record.SymbolsAdded, &record.SymbolsModified, &record.NetSymbolReduction,
			&structuralMetrics, &prep, &target, &refactor, &candidates,
		); err != nil {
			return nil, fmt.Errorf("scan legacy history sqlite: %w", err)
		}
		record.Timestamp = parseLegacyTimestamp(timestamp)
		record.Success = success != 0
		record.DryRun = dryRun != 0
		record.StructuralMetrics = structuralMetrics != 0
		record.Prep = decodeLegacyPrep(prep)
		record.Target = decodeLegacyTarget(target)
		record.Refactor = decodeLegacyRefactor(refactor)
		record.Candidates = decodeLegacyCandidates(candidates)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read legacy history sqlite: %w", err)
	}
	return records, nil
}

func legacySQLiteQuery(selects []string) string {
	return "SELECT " + strings.Join(selects, ", ") + " FROM runs ORDER BY jsonl_index"
}

func sqliteColumnOrFallback(columns map[string]bool, column, fallback string) string {
	if !columns[column] {
		return fallback
	}
	return "COALESCE(" + column + ", " + fallback + ")"
}

func parseLegacyTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed
	}
	parsed, _ = time.Parse(time.RFC3339, value)
	return parsed
}

func decodeLegacyPrep(value string) *PrepContext {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var context PrepContext
	if json.Unmarshal([]byte(value), &context) != nil {
		return nil
	}
	return &context
}

func decodeLegacyTarget(value string) *TargetContext {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var context TargetContext
	if json.Unmarshal([]byte(value), &context) != nil {
		return nil
	}
	return &context
}

func decodeLegacyRefactor(value string) *RefactorContext {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var context RefactorContext
	if json.Unmarshal([]byte(value), &context) != nil {
		return nil
	}
	return &context
}

func decodeLegacyCandidates(value string) *CandidatePacket {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var packet CandidatePacket
	if json.Unmarshal([]byte(value), &packet) != nil {
		return nil
	}
	return &packet
}

// syncDB replaces the derived SQLite mirror from the authoritative JSONL.
func (h *History) syncDB(ctx context.Context, db *sql.DB) error {
	records, err := h.loadJSONL()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin history mirror: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM runs"); err != nil {
		return fmt.Errorf("clear history mirror: %w", err)
	}
	for i, rec := range records {
		payload, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode history mirror: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO runs(jsonl_index, payload) VALUES (?, ?)", i, string(payload)); err != nil {
			return fmt.Errorf("write history mirror: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit history mirror: %w", err)
	}
	return nil
}

// RecordsForRepo filters records to one repository, matching by absolute
// path so relative and absolute spellings of the same target agree.
func RecordsForRepo(records []Record, repoPath string) []Record {
	abs, absErr := filepath.Abs(repoPath)
	relevant := make([]Record, 0, len(records))
	for _, rec := range records {
		if absErr == nil {
			if recAbs, err := filepath.Abs(rec.RepoPath); err == nil && recAbs == abs {
				relevant = append(relevant, rec)
				continue
			}
		}
		if rec.RepoPath == repoPath {
			relevant = append(relevant, rec)
		}
	}
	return relevant
}
