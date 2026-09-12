package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/eval"
)

func TestAppendOutcomeWritesJSONLWithPrivatePermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "outcomes.jsonl")
	record := eval.OutcomeRecord{Schema: eval.OutcomeSchema, ReportID: "report", EvidenceID: "evidence", Verdict: eval.VerdictConfirmed}
	if err := AppendOutcome(path)(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(contents), "\n") {
		t.Fatalf("outcome is not JSONL: %q", contents)
	}
	var got eval.OutcomeRecord
	if decodeErr := json.Unmarshal([]byte(strings.TrimSpace(string(contents))), &got); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if got != record {
		t.Fatalf("outcome = %#v, want %#v", got, record)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("outcome permissions = %o, want no group or other bits", info.Mode().Perm())
	}
}
