package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/render"
	"github.com/dotcommander/pan/internal/reportimport"
)

// ImportToolReport imports existing evidence without loading source or a snapshot.
// The envelope's complete flag describes parsing of supplied input only.
func ImportToolReport(ctx context.Context, format string, input io.Reader, outputFormat string) ([]byte, error) {
	return importToolReportBounded(ctx, format, input, outputFormat, 256<<20)
}

func importToolReportBounded(ctx context.Context, format string, input io.Reader, outputFormat string, outputLimit int) ([]byte, error) {
	report, err := reportimport.Import(ctx, format, input, reportimport.Limits{OutputBytes: outputLimit})
	if err != nil {
		return nil, err
	}
	envelope := render.NewEnvelope([]string{"review", "import", format}, "", analyze.Status{Complete: report.Import.Complete}, report, nil)
	// render.Write materializes JSON before calling its writer. Account for its
	// escaping and indentation first, rather than relying on the writer cap.
	var sizeErr error
	if outputFormat == "json" {
		sizeErr = reportimport.CheckJSONSize(ctx, envelope, outputLimit-1, true)
	} else {
		header := fmt.Sprintf("pan %s\ncommand: %s\nrepository: %s\ncomplete: %t\n\n", envelope.SchemaVersion, strings.Join(envelope.Command, " "), envelope.Repository, envelope.Analysis.Complete)
		sizeErr = reportimport.CheckJSONSize(ctx, report, outputLimit-len(header)-1, true)
	}
	if sizeErr != nil {
		return nil, sizeErr
	}
	writer := reportBuffer{ctx: ctx, limit: outputLimit}
	if err := render.Write(&writer, outputFormat, envelope); err != nil {
		return nil, fmt.Errorf("render imported report: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

type reportBuffer struct {
	bytes.Buffer
	ctx   context.Context
	limit int
}

func (b *reportBuffer) Write(data []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > b.limit-b.Len() {
		return 0, fmt.Errorf("rendered report exceeds output byte limit %d", b.limit)
	}
	return b.Buffer.Write(data)
}
