// Package agent implements pan's sequential JSONL protocol for coding
// agents. One request object per line, one response object per line. The
// protocol supports discovery, bounded review selection, and optional local
// outcome recording. It never contacts a provider or mutates the target repository.
package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/eval"
	"github.com/dotcommander/pan/internal/review"
	"io"
)

// Schema is the protocol identifier carried by every request and response.
const Schema = "pan.agent/v1"

// MaxRequestBytes bounds one request line.
const MaxRequestBytes = 1 << 20

const reportIDField = "report_id"

// Stable protocol error codes.
const (
	CodeInvalidRequest    = "invalid_request"
	CodeUnsupportedSchema = "unsupported_schema"
	CodeUnsupportedOp     = "unsupported_op"
	CodeRequestTooLarge   = "request_too_large"
	CodeScanFailed        = "scan_failed"
	CodeCanceled          = "canceled"
	CodeTargetNotFound    = "target_not_found"
	CodeFeedbackDisabled  = "feedback_disabled"
	CodeFeedbackWrite     = "feedback_write_failed"
	CodeStaleEvidence     = "stale_evidence"
	CodeBudgetTooSmall    = "budget_too_small"
	CodeBudgetTooLarge    = "budget_too_large"
)

// Stdio protocol operations.
const (
	OpHello    = "hello"
	OpReport   = "report"
	OpScan     = "scan"
	OpQuery    = "query"
	OpContext  = "context"
	OpFeedback = "feedback"
)

// OperationNames returns the complete stdio operation set in canonical
// order. It is a function so the protocol owns no mutable package state.
func OperationNames() []string {
	return []string{OpHello, OpScan, OpQuery, OpContext, OpFeedback, OpReport}
}

// Response is one protocol answer.
type Response struct {
	Schema      string       `json:"schema"`
	ID          string       `json:"id"`
	OK          bool         `json:"ok"`
	Result      any          `json:"result,omitempty"`
	ReportID    string       `json:"report_id,omitempty"`
	SourceState *SourceState `json:"source_state,omitempty"`
	Error       *Error       `json:"error,omitempty"`
}

// SourceState identifies the current filesystem snapshot. TreeDigest is
// the current deterministic report identity when no Git state is available.
type SourceState struct {
	Kind       string `json:"kind"`
	Head       string `json:"head"`
	Dirty      bool   `json:"dirty"`
	TreeDigest string `json:"tree_digest"`
}

// Error carries one stable error code.
type Error struct {
	Code string `json:"code"`
}

// ReportBuilder builds the deterministic review report document on demand.
// Implementations must be safe for repeated calls and must not mutate the
// target repository.
type ReportBuilder func(ctx context.Context) (review.Document, error)

// OutcomeWriter records one validated feedback label. It is nil unless the
// caller explicitly enables an outcome ledger.
type OutcomeWriter func(context.Context, eval.OutcomeRecord) error

// Run reads JSONL requests from input until EOF, answering each with one
// JSONL response on output. A build failure answers scan_failed per request
// without terminating the session; context cancellation answers canceled
// once and then terminates.
// Run reads JSONL requests from input until EOF, answering each with one
// JSONL response on output. A build failure answers scan_failed per request
// without terminating the session; context cancellation answers canceled
// once and then terminates.
func Run(ctx context.Context, input *bufio.Reader, output io.Writer, build ReportBuilder) error {
	return RunWithOutcomes(ctx, input, output, build, nil)
}

// RunWithOutcomes runs the protocol with an optional feedback writer.
func RunWithOutcomes(ctx context.Context, input *bufio.Reader, output io.Writer, build ReportBuilder, outcomes OutcomeWriter) error {
	return RunWithSession(ctx, input, output, Session{Build: build, Outcomes: outcomes})
}

// Session groups protocol dependencies for one stdio session.
type Session struct {
	Build    ReportBuilder
	Context  ContextBuilder
	Verify   SnapshotVerifier
	Outcomes OutcomeWriter
}

// RunWithSession serves one protocol session with caller-owned dependencies.
func RunWithSession(ctx context.Context, input *bufio.Reader, output io.Writer, session Session) error {
	engine := executor{build: session.Build, context: session.Context, verify: session.Verify, outcomes: session.Outcomes}
	for {
		line, oversized, err := readRecord(input)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read agent request: %w", err)
		}
		eof := errors.Is(err, io.EOF)
		if eof && len(line) == 0 && !oversized {
			return nil
		}
		if oversized {
			if writeErr := writeError(output, Schema, "", CodeRequestTooLarge); writeErr != nil {
				return writeErr
			}
			if eof {
				return nil
			}
			continue
		}
		if stop, stopErr := answerRecord(ctx, output, line, eof, engine); stop {
			return stopErr
		}
	}
}

// answerRecord answers one well-sized request line. It reports whether the
// session ended and, when it did, the session result.
func answerRecord(ctx context.Context, output io.Writer, line []byte, eof bool, engine executor) (bool, error) {
	req, code := decodeRequest(line)
	if code != "" {
		if writeErr := writeError(output, responseSchema(req.Schema), req.ID, code); writeErr != nil {
			return true, writeErr
		}
		return eof, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if writeErr := writeError(output, responseSchema(req.Schema), req.ID, CodeCanceled); writeErr != nil {
			return true, writeErr
		}
		return true, ctxErr
	}
	response, opErr := engine.execute(ctx, req)
	if opErr != nil {
		if writeErr := writeError(output, responseSchema(req.Schema), req.ID, opErrCode(opErr)); writeErr != nil {
			return true, writeErr
		}
		if errors.Is(opErr, context.Canceled) {
			return true, context.Canceled
		}
		return eof, nil
	}
	if writeErr := writeResponse(output, response); writeErr != nil {
		return true, writeErr
	}
	return eof, nil
}

// opErrCode maps one operation failure to its stable protocol code.
func opErrCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return CodeCanceled
	}
	if errors.Is(err, errTargetNotFound) {
		return CodeTargetNotFound
	}
	if errors.Is(err, errFeedbackDisabled) {
		return CodeFeedbackDisabled
	}
	if errors.Is(err, errFeedbackWrite) {
		return CodeFeedbackWrite
	}
	if errors.Is(err, ErrStaleEvidence) {
		return CodeStaleEvidence
	}
	if errors.Is(err, ErrContextBudgetTooSmall) {
		return CodeBudgetTooSmall
	}
	if errors.Is(err, ErrContextBudgetTooLarge) {
		return CodeBudgetTooLarge
	}
	return CodeScanFailed
}

// execute answers one decoded request.
type executor struct {
	build    ReportBuilder
	context  ContextBuilder
	verify   SnapshotVerifier
	outcomes OutcomeWriter
}

func (e executor) execute(ctx context.Context, req request) (Response, error) {
	if req.Op == OpHello {
		return helloResponse(req, e.outcomes), nil
	}
	doc, err := e.build(ctx)
	if err != nil {
		return Response{}, err
	}
	respond := responseFor(req, doc)
	switch req.Op {
	case OpReport:
		return respond(doc), nil
	case OpScan:
		return respond(scanResult(req, doc)), nil
	case OpQuery:
		return e.query(doc, req, respond)
	case OpContext:
		return e.contextResponse(ctx, doc, req, respond)
	case OpFeedback:
		return e.feedback(ctx, doc, req, respond)
	default:
		return Response{}, errors.New("unsupported operation")
	}
}

func helloResponse(req request, outcomes OutcomeWriter) Response {
	return Response{Schema: Schema, ID: req.ID, OK: true, Result: map[string]any{"protocol": Schema, "schema_version": analyze.SchemaVersion, "schemas": []string{review.DocumentSchema, review.CullLedgerSchema, ContextSchema, eval.OutcomeSchema}, "report_schema": review.DocumentSchema, "operations": OperationNames(), "max_request_bytes": MaxRequestBytes, "max_context_bytes": MaxRequestBytes, "scoring": "deterministic"}}
}

func responseFor(req request, doc review.Document) func(any) Response {
	return func(result any) Response {
		response := Response{Schema: responseSchema(req.Schema), ID: req.ID, OK: true, Result: result}
		return response
	}
}

func scanResult(req request, doc review.Document) map[string]any {
	schema := review.DocumentSchema
	return map[string]any{"schema": schema, reportIDField: doc.ReportID, "read_queue": doc.ReadQueue, "cull_ledger": review.BuildCullLedger(doc.ReadQueue)}
}

func (e executor) query(doc review.Document, req request, respond func(any) Response) (Response, error) {
	rows, err := selectRows(doc, req.Fields)
	if err != nil {
		return Response{}, err
	}
	return respond(map[string]any{reportIDField: doc.ReportID, "count": len(rows), "summaries": rows}), nil
}

func (e executor) contextResponse(ctx context.Context, doc review.Document, req request, respond func(any) Response) (Response, error) {
	budget, err := contextBudget(req.Fields)
	if err != nil {
		return Response{}, err
	}
	rows, err := selectRows(doc, req.Fields)
	if err != nil {
		return Response{}, err
	}
	if e.context == nil {
		return Response{}, errors.New("source context unavailable")
	}
	packet, err := e.context(ctx, doc, rows, budget)
	if err != nil {
		return Response{}, err
	}
	if err := e.verifySnapshot(ctx, doc); err != nil {
		return Response{}, err
	}
	return respond(packet), nil
}

func (e executor) feedback(ctx context.Context, doc review.Document, req request, respond func(any) Response) (Response, error) {
	record, err := feedbackRecord(doc, req.Fields)
	if err != nil {
		return Response{}, err
	}
	if e.outcomes == nil {
		return Response{}, errFeedbackDisabled
	}
	if err := e.verifySnapshot(ctx, doc); err != nil {
		return Response{}, err
	}
	if err := e.outcomes(ctx, record); err != nil {
		return Response{}, fmt.Errorf("%w: %w", errFeedbackWrite, err)
	}
	return respond(map[string]bool{"recorded": true}), nil
}

func (e executor) verifySnapshot(ctx context.Context, doc review.Document) error {
	if e.verify == nil {
		return nil
	}
	return e.verify(ctx, doc)
}

func responseSchema(schema string) string {
	return Schema
}

var (
	errTargetNotFound   = errors.New("agent target not found")
	errFeedbackDisabled = errors.New("agent feedback disabled")
	errFeedbackWrite    = errors.New("agent feedback write")
)
