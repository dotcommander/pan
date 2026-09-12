package agent

import (
	"context"
	"encoding/json"
	"io"
)

// MapStatus describes the current repository-map snapshot.
type MapStatus struct {
	BuiltAt string `json:"built_at"`
	Stale   bool   `json:"stale"`
	Root    string `json:"root"`
}
type mapRenderParams struct {
	Format string `json:"format"`
}
type symbolFindParams struct {
	Query string `json:"query"`
}
type fileExplainParams struct {
	Path string `json:"path"`
}
type fileContextParams struct {
	Query          string `json:"query"`
	Kind           string `json:"kind"`
	File           string `json:"file"`
	MaxSourceLines int    `json:"max_source_lines"`
}

func answerPanMap(ctx context.Context, output io.Writer, req serveRequest, backend ServeBackend) error {
	switch req.Method {
	case MethodMapRender:
		return answerMapRender(ctx, output, req, backend)
	case MethodMapStatus:
		return answerMapStatus(ctx, output, req, backend)
	case MethodSymbolFind:
		return answerSymbolFind(ctx, output, req, backend)
	case MethodFileExplain:
		return answerFileExplain(ctx, output, req, backend)
	case MethodFileContext:
		return answerFileContext(ctx, output, req, backend)
	default:
		return writeServeError(output, req.ID, ServeCodeMethodNotFound, "method not found")
	}
}

func answerMapRender(ctx context.Context, output io.Writer, req serveRequest, backend ServeBackend) error {
	var p mapRenderParams
	if failure := decodePanMapParams(req.Params, &p); failure != nil {
		return writeServeError(output, req.ID, failure.code, failure.message)
	}
	content, err := backend.AgentMapRender(ctx, p.Format)
	if err != nil {
		return serverError(output, req, err)
	}
	return writeServeResult(output, req.ID, map[string]string{"content": content})
}
func answerMapStatus(ctx context.Context, output io.Writer, req serveRequest, backend ServeBackend) error {
	var p struct{}
	if failure := decodePanMapParams(req.Params, &p); failure != nil {
		return writeServeError(output, req.ID, failure.code, failure.message)
	}
	result, err := backend.AgentMapStatus(ctx)
	if err != nil {
		return serverError(output, req, err)
	}
	return writeServeResult(output, req.ID, result)
}
func answerSymbolFind(ctx context.Context, output io.Writer, req serveRequest, backend ServeBackend) error {
	var p symbolFindParams
	if failure := decodePanMapParams(req.Params, &p); failure != nil || p.Query == "" {
		return invalidRequiredParam(output, req, "query")
	}
	matches, err := backend.AgentSymbolFind(ctx, p.Query)
	if err != nil {
		return serverError(output, req, err)
	}
	return writeServeResult(output, req.ID, map[string]any{"matches": matches})
}
func answerFileExplain(ctx context.Context, output io.Writer, req serveRequest, backend ServeBackend) error {
	var p fileExplainParams
	if failure := decodePanMapParams(req.Params, &p); failure != nil || p.Path == "" {
		return invalidRequiredParam(output, req, "path")
	}
	result, err := backend.AgentFileExplain(ctx, p.Path)
	if err != nil {
		return serverError(output, req, err)
	}
	return writeServeResult(output, req.ID, result)
}
func answerFileContext(ctx context.Context, output io.Writer, req serveRequest, backend ServeBackend) error {
	var p fileContextParams
	if failure := decodePanMapParams(req.Params, &p); failure != nil || p.Query == "" {
		return invalidRequiredParam(output, req, "query")
	}
	result, err := backend.AgentFileContext(ctx, p.Query, p.Kind, p.File, p.MaxSourceLines)
	if err != nil {
		return serverError(output, req, err)
	}
	return writeServeResult(output, req.ID, result)
}
func serverError(output io.Writer, req serveRequest, err error) error {
	return writeServeError(output, req.ID, ServeCodeServerError, boundedMessage(err))
}
func invalidRequiredParam(output io.Writer, req serveRequest, name string) error {
	return writeServeError(output, req.ID, ServeCodeInvalidParams, name+" must not be empty")
}
func decodePanMapParams(raw json.RawMessage, dst any) *serveFailure {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &serveFailure{code: ServeCodeInvalidParams, message: "invalid params"}
	}
	return nil
}
