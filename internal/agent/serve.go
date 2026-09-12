package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

// ServeJSONRPC is the JSON-RPC version agent serve speaks.
const ServeJSONRPC = "2.0"

// Serve method names. Every method derives from one deterministic, local
// analysis; none contacts a provider or mutates the target repository.
const (
	MethodStatus      = "pan/status"
	MethodOverview    = "pan/overview"
	MethodReport      = "pan/report"
	MethodSymbols     = "pan/symbols"
	MethodMapRender   = "map/render"
	MethodMapStatus   = "map/status"
	MethodSymbolFind  = "symbol/find"
	MethodFileExplain = "file/explain"
	MethodFileContext = "file/context"
)

// ServeMethodNames returns the complete agent serve method set in
// canonical order. It is a function so the protocol owns no mutable
// package state.
func ServeMethodNames() []string {
	return []string{MethodMapRender, MethodMapStatus, MethodSymbolFind, MethodFileExplain, MethodFileContext, MethodStatus, MethodOverview, MethodReport, MethodSymbols}
}

// pan/symbols parameter bounds.
const (
	ServeSymbolsDefaultTop = 50
	ServeSymbolsMaxTop     = 500
)

// JSON-RPC 2.0 error codes agent serve answers with.
const (
	ServeCodeParseError     = -32700
	ServeCodeInvalidRequest = -32600
	ServeCodeMethodNotFound = -32601
	ServeCodeInvalidParams  = -32602
	ServeCodeServerError    = -32000
)

// messageParseError is the stable message for unparseable request lines.
const messageParseError = "parse error"

// StatusSummary is the bounded pan/status result: one snapshot's shape.
type StatusSummary struct {
	Repository string   `json:"repository"`
	Schema     string   `json:"schema"`
	Files      int      `json:"files"`
	Symbols    int      `json:"symbols"`
	Edges      int      `json:"edges"`
	Complete   bool     `json:"complete"`
	Limits     []string `json:"limits,omitempty"`
}

// SymbolsResult is the bounded pan/symbols result.
type SymbolsResult struct {
	Total       int                  `json:"total"`
	Symbols     []analyze.Symbol     `json:"symbols"`
	Truncations []analyze.Truncation `json:"truncations,omitempty"`
}

// ServeBackend supplies the deterministic local data behind agent serve.
// Implementations must be read-only with respect to the target repository
// and safe for repeated calls.
type ServeBackend interface {
	AgentStatus(ctx context.Context) (StatusSummary, error)
	AgentOverview(ctx context.Context) (scan.OverviewReport, error)
	AgentSymbols(ctx context.Context, query string, top int) ([]analyze.Symbol, error)
	AgentReport(ctx context.Context) (review.Document, error)
	AgentMapRender(ctx context.Context, format string) (string, error)
	AgentMapStatus(ctx context.Context) (MapStatus, error)
	AgentSymbolFind(ctx context.Context, query string) (any, error)
	AgentFileExplain(ctx context.Context, path string) (any, error)
	AgentFileContext(ctx context.Context, query, kind, file string, maxSourceLines int) (any, error)
}

// serveRequest is one decoded JSON-RPC request. Fields retains the raw
// object so strict field-set validation can reject unknown names.
type serveRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Fields map[string]json.RawMessage
	hasID  bool
}

// serveFailure carries one protocol-level rejection.
type serveFailure struct {
	code    int
	message string
}

func (f serveFailure) Error() string { return f.message }

// RunServe reads NDJSON JSON-RPC 2.0 requests from input until EOF,
// answering each with one NDJSON response on output. Protocol failures
// answer with standard error codes without ending the session; context
// cancellation answers canceled once and then terminates.
func RunServe(ctx context.Context, input *bufio.Reader, output io.Writer, backend ServeBackend) error {
	for {
		line, oversized, err := readRecord(input)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read agent serve request: %w", err)
		}
		eof := errors.Is(err, io.EOF)
		if eof && len(line) == 0 && !oversized {
			return nil
		}
		if oversized {
			if writeErr := writeServeError(output, json.RawMessage("null"), ServeCodeParseError, "request too large"); writeErr != nil {
				return writeErr
			}
			if eof {
				return nil
			}
			continue
		}
		if stop, stopErr := answerServeRecord(ctx, output, line, eof, backend); stop {
			return stopErr
		}
	}
}

// answerServeRecord handles one well-sized request line. It reports
// whether the session ended and, when it did, the session result.
func answerServeRecord(ctx context.Context, output io.Writer, line []byte, eof bool, backend ServeBackend) (bool, error) {
	req, failure := decodeServeRequest(line)
	if failure != nil {
		if writeErr := writeServeError(output, serveFailureID(req), failure.code, failure.message); writeErr != nil {
			return true, writeErr
		}
		return eof, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if writeErr := writeServeError(output, req.ID, ServeCodeServerError, "canceled"); writeErr != nil {
			return true, writeErr
		}
		return true, ctxErr
	}
	return eof, answer(ctx, output, req, backend)
}

// serveFailureID echoes the request id when validation got far enough to
// decode one, and the JSON-RPC null id otherwise.
func serveFailureID(req serveRequest) json.RawMessage {
	if req.hasID {
		return req.ID
	}
	return json.RawMessage("null")
}

// answer executes one request and writes its response.
func answer(ctx context.Context, output io.Writer, req serveRequest, backend ServeBackend) error {
	var result any
	switch req.Method {
	case MethodMapRender, MethodMapStatus, MethodSymbolFind, MethodFileExplain, MethodFileContext:
		return answerPanMap(ctx, output, req, backend)
	case MethodStatus:
		summary, err := backend.AgentStatus(ctx)
		if err != nil {
			return writeServeError(output, req.ID, ServeCodeServerError, boundedMessage(err))
		}
		result = summary
	case MethodOverview:
		overview, err := backend.AgentOverview(ctx)
		if err != nil {
			return writeServeError(output, req.ID, ServeCodeServerError, boundedMessage(err))
		}
		result = overview
	case MethodReport:
		document, err := backend.AgentReport(ctx)
		if err != nil {
			return writeServeError(output, req.ID, ServeCodeServerError, boundedMessage(err))
		}
		result = document
	case MethodSymbols:
		query, top, failure := decodeSymbolsParams(req.Params)
		if failure != nil {
			return writeServeError(output, req.ID, failure.code, failure.message)
		}
		symbols, err := backend.AgentSymbols(ctx, query, top)
		if err != nil {
			return writeServeError(output, req.ID, ServeCodeServerError, boundedMessage(err))
		}
		result = boundServeSymbols(symbols)
	default:
		return writeServeError(output, req.ID, ServeCodeMethodNotFound, "method not found: "+req.Method)
	}
	return writeServeResult(output, req.ID, result)
}

// decodeServeRequest strictly decodes one request line: exactly one JSON
// object, only the canonical fields, jsonrpc 2.0, a string or number id,
// and a non-empty method.
func decodeServeRequest(line []byte) (serveRequest, *serveFailure) {
	req := serveRequest{Fields: map[string]json.RawMessage{}}
	if failure := decodeServeObject(line, &req); failure != nil {
		return req, failure
	}
	if failure := rejectUnknownServeFields(req.Fields); failure != nil {
		return req, failure
	}
	if failure := decodeServeHeader(&req); failure != nil {
		return req, failure
	}
	if failure := decodeServeMethod(&req); failure != nil {
		return req, failure
	}
	return req, decodeServeParams(&req)
}

// decodeServeObject decodes exactly one JSON object from the line.
func decodeServeObject(line []byte, req *serveRequest) *serveFailure {
	decoder := json.NewDecoder(bytes.NewReader(line))
	if err := decoder.Decode(&req.Fields); err != nil || req.Fields == nil {
		return parseFailure()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parseFailure()
	}
	return nil
}

// parseFailure is the shared unparseable-line rejection.
func parseFailure() *serveFailure {
	return &serveFailure{code: ServeCodeParseError, message: messageParseError}
}

// rejectUnknownServeFields enforces the canonical request field set.
func rejectUnknownServeFields(fields map[string]json.RawMessage) *serveFailure {
	for name := range fields {
		switch name {
		case "jsonrpc", "id", "method", "params":
		default:
			return &serveFailure{code: ServeCodeInvalidRequest, message: "unknown field " + name}
		}
	}
	return nil
}

// decodeServeHeader validates the jsonrpc version and correlation id,
// recording the id so failures can still echo it.
func decodeServeHeader(req *serveRequest) *serveFailure {
	var version string
	if err := decodeServeString(req.Fields, "jsonrpc", &version); err != nil || version != ServeJSONRPC {
		return &serveFailure{code: ServeCodeInvalidRequest, message: "jsonrpc must be \"2.0\""}
	}
	raw, ok := req.Fields["id"]
	if !ok {
		return &serveFailure{code: ServeCodeInvalidRequest, message: "id is required"}
	}
	req.hasID = true
	req.ID = raw
	if !validServeID(raw) {
		return &serveFailure{code: ServeCodeInvalidRequest, message: "id must be a string or number"}
	}
	return nil
}

// decodeServeMethod decodes the required non-empty method name.
func decodeServeMethod(req *serveRequest) *serveFailure {
	method, ok := req.Fields["method"]
	if !ok {
		return &serveFailure{code: ServeCodeInvalidRequest, message: "method is required"}
	}
	if err := json.Unmarshal(method, &req.Method); err != nil || req.Method == "" {
		return &serveFailure{code: ServeCodeInvalidRequest, message: "method must be a non-empty string"}
	}
	return nil
}

// decodeServeParams decodes the optional params object.
func decodeServeParams(req *serveRequest) *serveFailure {
	raw, ok := req.Fields["params"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return &serveFailure{code: ServeCodeInvalidParams, message: "params must be an object"}
	}
	req.Params = raw
	return nil
}

// validServeID accepts string and number ids (not null, bool, array, or
// object) so every response can echo its request correlation verbatim.
func validServeID(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false
	}
	switch trimmed[0] {
	case '"':
		var value string
		return json.Unmarshal(raw, &value) == nil
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var value float64
		return json.Unmarshal(raw, &value) == nil
	default:
		return false
	}
}

// decodeServeString decodes one required string field.
func decodeServeString(fields map[string]json.RawMessage, name string, destination *string) error {
	raw, ok := fields[name]
	if !ok {
		return errors.New("missing field")
	}
	return json.Unmarshal(raw, destination)
}

// decodeSymbolsParams decodes the pan/symbols parameter object: an
// optional query string and an optional top bound with range enforcement.
func decodeSymbolsParams(raw json.RawMessage) (string, int, *serveFailure) {
	query := ""
	top := ServeSymbolsDefaultTop
	if len(raw) == 0 {
		return query, top, nil
	}
	var params struct {
		Query *string `json:"query"`
		Top   *int    `json:"top"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", 0, &serveFailure{code: ServeCodeInvalidParams, message: "params must be an object with optional query and top"}
	}
	if params.Query != nil {
		query = *params.Query
	}
	if params.Top != nil {
		top = *params.Top
	}
	if top < 1 || top > ServeSymbolsMaxTop {
		return "", 0, &serveFailure{code: ServeCodeInvalidParams, message: fmt.Sprintf("top must be between 1 and %d", ServeSymbolsMaxTop)}
	}
	return query, top, nil
}

// boundServeSymbols caps the symbol list and records any cut.
func boundServeSymbols(symbols []analyze.Symbol) SymbolsResult {
	shown := symbols
	if len(shown) > ServeSymbolsMaxTop {
		shown = shown[:ServeSymbolsMaxTop]
	}
	result := SymbolsResult{Total: len(symbols), Symbols: shown}
	if len(symbols) > ServeSymbolsMaxTop {
		result.Truncations = append(result.Truncations, analyze.Truncation{
			Field: "symbols", Shown: ServeSymbolsMaxTop, Total: len(symbols), Reason: "symbol cap",
		})
	}
	return result
}

// boundedMessage keeps one server-error message short and single-line.
func boundedMessage(err error) string {
	message := err.Error()
	if idx := strings.IndexByte(message, '\n'); idx >= 0 {
		message = message[:idx]
	}
	const maxMessage = 200
	if len(message) > maxMessage {
		message = message[:maxMessage]
	}
	return message
}

// serveSuccess is one successful JSON-RPC response.
type serveSuccess struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// serveErrorObject is one JSON-RPC error object.
type serveErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// serveErrorResponse is one failed JSON-RPC response.
type serveErrorResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id"`
	Error   serveErrorObject `json:"error"`
}

func writeServeResult(output io.Writer, id json.RawMessage, result any) error {
	return writeServeLine(output, serveSuccess{JSONRPC: ServeJSONRPC, ID: id, Result: result})
}

func writeServeError(output io.Writer, id json.RawMessage, code int, message string) error {
	return writeServeLine(output, serveErrorResponse{
		JSONRPC: ServeJSONRPC, ID: id, Error: serveErrorObject{Code: code, Message: message},
	})
}

func writeServeLine(output io.Writer, message any) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode agent serve response: %w", err)
	}
	encoded = append(encoded, '\n')
	written, err := output.Write(encoded)
	if err != nil {
		return fmt.Errorf("write agent serve response: %w", err)
	}
	if written != len(encoded) {
		return fmt.Errorf("write agent serve response: %w", io.ErrShortWrite)
	}
	return nil
}
