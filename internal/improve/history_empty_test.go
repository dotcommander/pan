package improve

import (
	"os"
	"testing"
)

func TestHistoryEmptyJSONLRemainsAuthoritativeOverSQLite(t *testing.T) {
	t.Parallel()
	history := NewHistory(HistoryPath(t.TempDir()))
	if err := history.Append(Record{RunType: RunTypeRefactor, RepoPath: "/repos/demo", Outcome: string(OutcomeSuccess)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history.Path(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %d, want empty authoritative JSONL", len(records))
	}
	if stale, err := history.loadSQLite(); err != nil || len(stale) != 0 {
		t.Fatalf("sqlite mirror = %d, %v", len(stale), err)
	}
}
