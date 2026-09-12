package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

// Capability names for the four bounded language-server queries.
const (
	CapabilityRefs    = "refs"
	CapabilityDef     = "def"
	CapabilityHover   = "hover"
	CapabilitySymbols = "symbols"
)

// Stable unavailable reasons. A typed unavailable result is a completed,
// deterministic answer about the environment — never a command error.
const (
	ReasonFileMissing     = "file_missing"
	ReasonLanguageUnknown = "language_not_configured"
	ReasonServerMissing   = "server_not_installed"
	ReasonFileTooLarge    = "file_exceeds_open_limit"
	ReasonServerStart     = "server_start_failed"
	ReasonQueryFailed     = "query_failed"
)

// Result caps are mechanism bounds, not user configuration: they keep one
// query's answer bounded no matter what the server returns.
const (
	MaxLocations  = 200
	MaxSymbols    = 500
	MaxHoverChars = 4000
	maxOpenBytes  = 1 << 20
)

// QueryResult is the typed answer to one language-server query. Status is
// "ok" or "unavailable"; every unavailable answer carries a stable reason
// and, where useful, a bounded detail string.
type QueryResult struct {
	Status      string               `json:"status"`
	Capability  string               `json:"capability"`
	Reason      string               `json:"reason,omitempty"`
	Detail      string               `json:"detail,omitempty"`
	Language    string               `json:"language,omitempty"`
	Server      string               `json:"server,omitempty"`
	File        string               `json:"file,omitempty"`
	Line        int                  `json:"line"`
	Column      int                  `json:"column"`
	Locations   []ResultLocation     `json:"locations,omitempty"`
	Hover       *HoverDetail         `json:"hover,omitempty"`
	Symbols     []SymbolRow          `json:"symbols,omitempty"`
	Truncations []analyze.Truncation `json:"truncations,omitempty"`
}

// ResultLocation is one resolved location as a filesystem path plus range.
type ResultLocation struct {
	Path  string `json:"path"`
	Range Range  `json:"range"`
}

// HoverDetail is the bounded hover payload.
type HoverDetail struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Truncated bool   `json:"truncated,omitempty"`
	Range     *Range `json:"range,omitempty"`
}

// SymbolRow is one flattened document symbol row.
type SymbolRow struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Detail      string `json:"detail,omitempty"`
	Depth       int    `json:"depth"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

// Service runs bounded language-server detection and queries under one
// configuration. It is read-only with respect to the target repository:
// servers are local subprocesses and queries never write.
type Service struct {
	rules    config.LspRules
	excludes []string
	lookPath func(string) (string, error)
	dial     func(ctx context.Context, command string) (*Client, error)
}

// NewService normalizes rules and excludes and installs the default
// command resolver and subprocess dialer.
func NewService(rules config.LspRules, excludes []string) *Service {
	s := &Service{
		rules:    rules.Normalized(),
		excludes: config.NormalizeExcludes(excludes),
	}
	s.lookPath = exec.LookPath
	s.dial = func(ctx context.Context, command string) (*Client, error) {
		return Start(ctx, command)
	}
	return s
}

// Status reports language-server coverage for root.
func (s *Service) Status(ctx context.Context, root string) (StatusReport, error) {
	return DetectStatus(ctx, root, s.rules, s.excludes, s.lookPath)
}

// QueryRequest names one bounded query: the capability, the target file,
// and the 0-based line and UTF-16 column inside it (symbols uses only the
// file).
type QueryRequest struct {
	Capability string
	File       string
	Line       int
	Column     int
}

// Query answers one capability query for the symbol at the request's
// file:line:column. The returned result is always a typed answer; the
// error is reserved for caller misuse (unknown capability) rather than
// environment facts.
func (s *Service) Query(ctx context.Context, root string, req QueryRequest) (QueryResult, error) {
	if !isValidCapability(req.Capability) {
		return QueryResult{}, fmt.Errorf("lsp: unknown capability %q", req.Capability)
	}
	result := QueryResult{Status: "unavailable", Capability: req.Capability, Line: req.Line, Column: req.Column}
	absFile, err := filepath.Abs(req.File)
	if err != nil {
		return QueryResult{}, fmt.Errorf("lsp: resolve file: %w", err)
	}
	result.File = absFile

	env, ok := s.resolveQueryEnv(&result, absFile)
	if !ok {
		return result, nil
	}
	result.Language = env.language.Language
	result.Server = env.command

	client, err := s.dial(ctx, env.command)
	if err != nil {
		result.Reason = ReasonServerStart
		result.Detail = boundedDetail(err.Error())
		return result, nil
	}
	defer func() {
		_ = client.Shutdown(context.Background())
	}()

	qctx, cancel := context.WithTimeout(ctx, s.rules.Timeout)
	defer cancel()
	if err := openDocument(qctx, client, root, absFile, env); err != nil {
		return queryFailed(result, err), nil
	}
	query := capabilityQuery{client: client, capability: req.Capability, file: absFile, line: req.Line, column: req.Column}
	if err := runCapability(qctx, &result, query); err != nil {
		return queryFailed(result, err), nil
	}
	result.Status = "ok"
	return result, nil
}

// isValidCapability reports whether capability is one of the four bounded
// language-server queries.
func isValidCapability(capability string) bool {
	switch capability {
	case CapabilityRefs, CapabilityDef, CapabilityHover, CapabilitySymbols:
		return true
	}
	return false
}

// queryEnv is the resolved query environment: the configured language, the
// available server command, and the bounded document content.
type queryEnv struct {
	language config.LspLanguage
	command  string
	content  []byte
}

// resolveQueryEnv validates the target file and its language, resolves an
// available server command, and reads the bounded document content. ok is
// false when result already carries the typed unavailable reason the caller
// must return verbatim.
func (s *Service) resolveQueryEnv(result *QueryResult, file string) (queryEnv, bool) {
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		result.Reason = ReasonFileMissing
		return queryEnv{}, false
	}
	lang, ok := s.languageFor(filepath.Ext(file))
	if !ok {
		result.Reason = ReasonLanguageUnknown
		result.Detail = fmt.Sprintf("no configured language covers %s", strings.ToLower(filepath.Ext(file)))
		return queryEnv{}, false
	}
	command, ok := firstAvailable(lang.Servers, s.lookPath)
	if !ok {
		result.Reason = ReasonServerMissing
		result.Detail = "tried: " + strings.Join(lang.Servers, ", ")
		return queryEnv{}, false
	}
	if info.Size() > maxOpenBytes {
		result.Reason = ReasonFileTooLarge
		result.Detail = fmt.Sprintf("file is %d bytes; didOpen is capped at %d", info.Size(), maxOpenBytes)
		return queryEnv{}, false
	}
	content, err := os.ReadFile(file)
	if err != nil {
		result.Reason = ReasonFileMissing
		result.Detail = err.Error()
		return queryEnv{}, false
	}
	return queryEnv{language: lang, command: command, content: content}, true
}

// openDocument initializes the session and publishes the document content
// so the server can answer queries about the file.
func openDocument(qctx context.Context, client *Client, root, file string, env queryEnv) error {
	if err := client.Initialize(qctx, root); err != nil {
		return err
	}
	return client.DidOpen(qctx, file, env.language.Language, string(env.content))
}

// capabilityQuery is one capability query bound to its session client and
// target position.
type capabilityQuery struct {
	client     *Client
	capability string
	file       string
	line       int
	column     int
}

// runCapability performs the capability query and stores its bounded answer
// into result.
func runCapability(qctx context.Context, result *QueryResult, query capabilityQuery) error {
	switch query.capability {
	case CapabilityRefs:
		locations, err := query.client.References(qctx, query.file, query.line, query.column)
		if err != nil {
			return err
		}
		result.Locations, result.Truncations = boundLocations(locations)
	case CapabilityDef:
		locations, err := query.client.Definition(qctx, query.file, query.line, query.column)
		if err != nil {
			return err
		}
		result.Locations, result.Truncations = boundLocations(locations)
	case CapabilityHover:
		hover, err := query.client.Hover(qctx, query.file, query.line, query.column)
		if err != nil {
			return err
		}
		result.Hover = boundHover(hover)
	case CapabilitySymbols:
		symbols, err := query.client.DocumentSymbols(qctx, query.file)
		if err != nil {
			return err
		}
		result.Symbols, result.Truncations = boundSymbols(symbols)
	}
	return nil
}

// queryFailed converts one query error into a typed unavailable result.
func queryFailed(result QueryResult, err error) QueryResult {
	result.Reason = ReasonQueryFailed
	result.Detail = boundedDetail(err.Error())
	return result
}

// languageFor resolves one file extension to its configured language.
// Languages are scanned in configuration order so the first entry covering
// an extension wins deterministically.
func (s *Service) languageFor(ext string) (config.LspLanguage, bool) {
	ext = strings.ToLower(ext)
	for _, lang := range s.rules.Languages {
		if slices.Contains(lang.FileTypes, ext) {
			return lang, true
		}
	}
	return config.LspLanguage{}, false
}

// boundLocations caps locations and records any cut.
func boundLocations(locations []Location) ([]ResultLocation, []analyze.Truncation) {
	out := make([]ResultLocation, 0, min(len(locations), MaxLocations))
	for _, location := range locations {
		if len(out) == MaxLocations {
			break
		}
		out = append(out, ResultLocation{Path: URIToPath(location.URI), Range: location.Range})
	}
	var truncations []analyze.Truncation
	if len(locations) > MaxLocations {
		truncations = append(truncations, analyze.Truncation{
			Field: "locations", Shown: MaxLocations, Total: len(locations), Reason: "location cap",
		})
	}
	return out, truncations
}

// boundHover caps the hover payload and records the cut.
func boundHover(hover *HoverResult) *HoverDetail {
	if hover == nil {
		return nil
	}
	detail := &HoverDetail{Kind: hover.Contents.Kind, Value: hover.Contents.Value, Range: hover.Range}
	if len(detail.Value) > MaxHoverChars {
		detail.Value = detail.Value[:MaxHoverChars]
		detail.Truncated = true
	}
	return detail
}

// boundSymbols flattens the document symbol tree depth-first and caps it.
func boundSymbols(symbols []DocumentSymbol) ([]SymbolRow, []analyze.Truncation) {
	rows := make([]SymbolRow, 0, min(countSymbols(symbols), MaxSymbols))
	var walk func(symbols []DocumentSymbol, depth int)
	walk = func(symbols []DocumentSymbol, depth int) {
		for _, symbol := range symbols {
			if len(rows) == MaxSymbols {
				return
			}
			rows = append(rows, SymbolRow{
				Name:        symbol.Name,
				Kind:        symbol.Kind.String(),
				Detail:      symbol.Detail,
				Depth:       depth,
				StartLine:   symbol.Range.Start.Line,
				StartColumn: symbol.Range.Start.Character,
				EndLine:     symbol.Range.End.Line,
				EndColumn:   symbol.Range.End.Character,
			})
			walk(symbol.Children, depth+1)
		}
	}
	walk(symbols, 0)
	var truncations []analyze.Truncation
	if total := countSymbols(symbols); total > MaxSymbols {
		truncations = append(truncations, analyze.Truncation{
			Field: "symbols", Shown: MaxSymbols, Total: total, Reason: "symbol cap",
		})
	}
	return rows, truncations
}

// countSymbols counts the full tree size.
func countSymbols(symbols []DocumentSymbol) int {
	total := 0
	for _, symbol := range symbols {
		total += 1 + countSymbols(symbol.Children)
	}
	return total
}

// boundedDetail keeps one error detail string short and single-line.
func boundedDetail(detail string) string {
	if idx := strings.IndexByte(detail, '\n'); idx >= 0 {
		detail = detail[:idx]
	}
	const maxDetail = 200
	if len(detail) > maxDetail {
		detail = detail[:maxDetail]
	}
	return detail
}
