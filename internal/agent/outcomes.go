package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dotcommander/pan/internal/eval"
)

// AppendOutcome writes one privacy-minimal eval ledger record. Callers opt
// into this mutation explicitly by supplying a ledger path to the CLI.
func AppendOutcome(path string) OutcomeWriter {
	return func(ctx context.Context, record eval.OutcomeRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create outcome directory: %w", err)
		}
		data, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode outcome: %w", err)
		}
		data = append(data, '\n')
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			return fmt.Errorf("open outcome ledger: %w", err)
		}
		defer func() { _ = file.Close() }()
		if _, err := file.Write(data); err != nil {
			return fmt.Errorf("append outcome: %w", err)
		}
		return nil
	}
}
