package retrievalstats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendAndSummarizeBuckets(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	today := now.Add(-time.Hour)
	lastWeek := now.AddDate(0, 0, -3)
	older := now.AddDate(0, 0, -30)

	records := []struct {
		at   time.Time
		file int
		pack int
	}{
		{today, 40_000, 4_000},
		{lastWeek, 20_000, 2_000},
		{older, 10_000, 1_000},
	}
	for _, r := range records {
		if err := Append(base, Record{TS: r.at.Unix(), Root: "/repo", Goal: "g", Targets: 2, PacketChars: r.pack, FileChars: r.file}); err != nil {
			t.Fatal(err)
		}
	}
	// append (not overwrite) one malformed line after the valid records
	ledger, err := os.OpenFile(Path(base), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	summary, err := Summarize(Path(base), now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.AllTime.Calls != 3 || summary.AllTime.SavedChars != 63_000 {
		t.Fatalf("all time = %+v", summary.AllTime)
	}
	if summary.Today.Calls != 1 || summary.Today.SavedChars != 36_000 || summary.Today.SavedTokens() != 9_000 {
		t.Fatalf("today = %+v", summary.Today)
	}
	if summary.LastWeek.Calls != 2 {
		t.Fatalf("last week = %+v", summary.LastWeek)
	}
	if summary.Malformed != 1 {
		t.Fatalf("malformed = %d", summary.Malformed)
	}
	if text := Render(summary); !containsAll(text, "Today", "Last 7 days", "All time", "9000") {
		t.Fatalf("render = %q", text)
	}
}

func TestSummarizeMissingLedgerIsEmpty(t *testing.T) {
	t.Parallel()
	summary, err := Summarize(filepath.Join(t.TempDir(), "absent.jsonl"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if summary.AllTime.Calls != 0 || summary.Malformed != 0 {
		t.Fatalf("summary = %+v", summary)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}
