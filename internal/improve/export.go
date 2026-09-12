package improve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

const RefactorObservationSchema = "pan.refactor-observation/v1"

const MaxObservationExport = 10000

type RefactorObservation struct {
	Schema string `json:"schema"`
	Record Record `json:"record"`
}

// ExportObservations reads the authoritative JSONL ledger without touching its
// SQLite mirror or executing any workflow.
func ExportObservations(path string, limit int, since *time.Time) ([]RefactorObservation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxObservationExport {
		return nil, fmt.Errorf("limit exceeds hard bound")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []RefactorObservation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("history line %d: %w", line, err)
		}
		if r.Schema != HistorySchema {
			return nil, fmt.Errorf("history line %d: unsupported schema %q", line, r.Schema)
		}
		if since != nil && r.Timestamp.Before(*since) {
			continue
		}
		out = append(out, RefactorObservation{Schema: RefactorObservationSchema, Record: r})
		if len(out) >= limit {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func WriteObservations(output string, observations []RefactorObservation) error {
	if output == "" {
		return fmt.Errorf("output path is required")
	}
	var data []byte
	for _, observation := range observations {
		line, err := json.Marshal(observation)
		if err != nil {
			return err
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	return atomicfile.Write(output, data, 0o600)
}
