package reportimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
)

// Import reads only the explicitly supplied stream. No embedded path is opened.
func Import(ctx context.Context, format string, input io.Reader, limits Limits) (Report, error) {
	l := limits.defaults()
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if input == nil {
		return Report{}, fmt.Errorf("report input is required")
	}
	if format != "ast-grep" && format != "jscpd" && format != "scip" {
		return Report{}, fmt.Errorf("unsupported report format %q; use ast-grep, jscpd or scip", format)
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, input}, l.InputBytes+1))
	if err != nil {
		return Report{}, fmt.Errorf("read report: %w", err)
	}
	if int64(len(data)) > l.InputBytes {
		return Report{}, fmt.Errorf("report exceeds input byte limit %d", l.InputBytes)
	}
	digest := sha256.Sum256(data)
	r := Report{
		Schema:      "tool-report/v1",
		Origin:      Origin{Format: format, InputSHA256: hex.EncodeToString(digest[:]), InputBytes: len(data)},
		Import:      ImportStatus{Complete: true, Scope: "captured input only"},
		Source:      SourceStatus{"unknown", "unknown", "unknown"},
		Ranking:     "path, range, symbol, original ordinal; no confidence score",
		Limitations: []string{"Input completeness does not establish repository coverage or source freshness.", "Additive upstream fields are retained as inert evidence, not interpreted instructions."},
		Unsupported: []string{}, Views: []View{},
	}
	switch format {
	case "ast-grep":
		err = importAst(ctx, data, l, &r)
	case "jscpd":
		err = importClones(ctx, data, l, &r)
	case "scip":
		err = importSCIP(ctx, data, l, &r)
	}
	if err != nil {
		return Report{}, fmt.Errorf("import %s: %w", format, err)
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	slices.SortStableFunc(r.Views, func(a, b View) int {
		if a.Path != b.Path {
			if a.Path < b.Path {
				return -1
			}
			return 1
		}
		if n := slices.Compare(a.Range, b.Range); n != 0 {
			return n
		}
		if a.Symbol != b.Symbol {
			if a.Symbol < b.Symbol {
				return -1
			}
			return 1
		}
		if a.DocumentOrdinal != nil && b.DocumentOrdinal != nil && *a.DocumentOrdinal != *b.DocumentOrdinal {
			return *a.DocumentOrdinal - *b.DocumentOrdinal
		}
		return a.Ordinal - b.Ordinal
	})
	slices.Sort(r.Unsupported)
	r.Unsupported = slices.Compact(r.Unsupported)
	return r, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
