// Package retrievalstats records optional per-call token savings for
// task-packet retrieval: the encoded packet size against the full-read size
// of the files it covers. Records are append-only JSONL under the pan cache
// directory; nothing inside the analyzed repository is written.
package retrievalstats

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileName is the savings ledger file inside the pan cache directory.
const FileName = "retrieval-savings.jsonl"

// Record is one persisted savings observation.
type Record struct {
	TS          int64  `json:"ts"`
	Root        string `json:"root"`
	Goal        string `json:"goal"`
	Targets     int    `json:"targets"`
	PacketChars int    `json:"packet_chars"`
	FileChars   int    `json:"file_chars"`
}

// Bucket aggregates calls and saved characters for one period.
type Bucket struct {
	Calls       int `json:"calls"`
	PacketChars int `json:"packet_chars"`
	FileChars   int `json:"file_chars"`
	// SavedChars is the positive part of FileChars - PacketChars.
	SavedChars int `json:"saved_chars"`
}

func (b *Bucket) add(record Record) {
	b.Calls++
	b.PacketChars += record.PacketChars
	b.FileChars += record.FileChars
	b.SavedChars += max(0, record.FileChars-record.PacketChars)
}

// SavedTokens estimates saved tokens at four characters per token.
func (b Bucket) SavedTokens() int { return b.SavedChars / 4 }

// Summary is the period-aggregated savings report.
type Summary struct {
	Today     Bucket `json:"today"`
	LastWeek  Bucket `json:"last_7_days"`
	AllTime   Bucket `json:"all_time"`
	Malformed int    `json:"malformed_lines,omitempty"`
}

// Path returns the ledger location under one cache base directory.
func Path(base string) string { return filepath.Join(base, FileName) }

// Append writes one savings observation to the ledger under base.
func Append(base string, record Record) error {
	if base == "" {
		return errors.New("savings cache base must not be blank")
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		return fmt.Errorf("create savings cache directory: %w", err)
	}
	if record.TS == 0 {
		record.TS = time.Now().UTC().Unix()
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode savings record: %w", err)
	}
	file, err := os.OpenFile(Path(base), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open savings ledger: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append savings record: %w", err)
	}
	return nil
}

// Summarize reads the ledger and aggregates it into period buckets relative
// to now. Malformed lines are skipped and counted, not fatal.
func Summarize(path string, now time.Time) (Summary, error) {
	summary := Summary{}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return summary, nil
		}
		return summary, fmt.Errorf("open savings ledger: %w", err)
	}
	defer func() { _ = file.Close() }()
	today := now.UTC().Format("2006-01-02")
	weekAgo := now.UTC().AddDate(0, 0, -7).Format("2006-01-02")
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			summary.Malformed++
			continue
		}
		day := time.Unix(record.TS, 0).UTC().Format("2006-01-02")
		summary.AllTime.add(record)
		if day > weekAgo {
			summary.LastWeek.add(record)
		}
		if day == today {
			summary.Today.add(record)
		}
	}
	if err := scanner.Err(); err != nil {
		return summary, fmt.Errorf("read savings ledger: %w", err)
	}
	return summary, nil
}

// Render formats one savings summary as the text report shown by the CLI.
func Render(summary Summary) string {
	rows := []struct {
		label  string
		bucket Bucket
	}{
		{"Today", summary.Today},
		{"Last 7 days", summary.LastWeek},
		{"All time", summary.AllTime},
	}
	out := "Retrieval token savings\n"
	for _, row := range rows {
		percent := 0
		if row.bucket.FileChars > 0 {
			percent = row.bucket.SavedChars * 100 / row.bucket.FileChars
		}
		out += fmt.Sprintf("  %-12s %5d calls  ~%d tokens saved (%d%%)\n", row.label, row.bucket.Calls, row.bucket.SavedTokens(), percent)
	}
	if summary.Malformed > 0 {
		out += fmt.Sprintf("  %d malformed ledger line(s) skipped\n", summary.Malformed)
	}
	return out
}
