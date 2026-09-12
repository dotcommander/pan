package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/dotcommander/pan/internal/agent"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/codemap"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/repo"
	"github.com/dotcommander/pan/internal/retrieval"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

// AgentServeState derives agent responses from a verified repository snapshot.
// It reuses unchanged evidence and rebuilds it before a request after content
// changes, including same-size files with preserved timestamps.
type AgentServeState struct {
	service     Service
	root        string
	mu          sync.Mutex
	ready       bool
	snap        analyze.Snapshot
	sources     map[string]agentSource
	redacted    map[string]bool
	hashes      map[string]string
	stamps      map[string]analyze.FileStamp
	fingerprint string
	builtAt     time.Time
	err         error
}

// AgentServe returns the serve state for one target repository. The
// snapshot is not built until the first method call.
func (s Service) AgentServe(root string) *AgentServeState {
	return &AgentServeState{service: s, root: absPath(root)}
}

// snapshot reuses verified evidence or rebuilds it before answering a request.
func (a *AgentServeState) snapshot(ctx context.Context) (analyze.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ready && a.err == nil && a.sourcesFresh(ctx) {
		return a.snap, nil
	}
	a.snap, a.err = a.service.Snapshot(ctx, a.root)
	if a.err == nil {
		a.err = a.captureSources(ctx, a.snap)
	}
	if a.err == nil {
		a.builtAt = time.Now().UTC()
		a.ready = true
	}
	return a.snap, a.err
}

func (a *AgentServeState) rankedSnapshot(ctx context.Context) (analyze.Snapshot, []ranking.RankedFile, error) {
	snap, err := a.snapshot(ctx)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	return snap, ranking.Rank(snap, repo.ModulePath(snap.Root), ranking.Options{}), nil
}

// AgentMapRender renders the current repository map in the requested format.
func (a *AgentServeState) AgentMapRender(ctx context.Context, format string) (string, error) {
	snap, ranked, err := a.rankedSnapshot(ctx)
	if err != nil {
		return "", err
	}
	mode := map[string]string{"": codemap.ModeEnriched, "compact": codemap.ModeCompact, "verbose": codemap.ModeVerbose, "detail": codemap.ModeDetail, "lines": codemap.ModeLines, "xml": codemap.ModeXML, "structured": codemap.ModeEnriched}[format]
	if mode == "" {
		return "", errors.New("unknown format")
	}
	result := codemap.Build(ranked, codemap.Options{Mode: mode, Root: snap.Root, IncludeTests: true})
	if format == "structured" {
		data, err := json.Marshal(result)
		return string(data), err
	}
	return result.Text, nil
}

// AgentMapStatus reports when the current repository evidence was built.
func (a *AgentServeState) AgentMapStatus(ctx context.Context) (agent.MapStatus, error) {
	snap, err := a.snapshot(ctx)
	if err != nil {
		return agent.MapStatus{}, err
	}
	built := ""
	if !a.builtAt.IsZero() {
		built = a.builtAt.Format(time.RFC3339)
	}
	return agent.MapStatus{BuiltAt: built, Root: snap.Root}, nil
}

// AgentSymbolFind resolves a symbol query against current evidence.
func (a *AgentServeState) AgentSymbolFind(ctx context.Context, query string) (any, error) {
	_, ranked, err := a.rankedSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	parsed := retrieval.ParseFindQuery(query)
	return retrieval.Find(ranked, parsed.Name, parsed.Kind, parsed.File), nil
}

// AgentFileExplain returns ranked evidence for one file.
func (a *AgentServeState) AgentFileExplain(ctx context.Context, path string) (any, error) {
	_, ranked, err := a.rankedSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return retrieval.Explain(ranked, path, 0)
}

// AgentFileContext returns bounded source and call context for a symbol.
func (a *AgentServeState) AgentFileContext(ctx context.Context, query, kind, file string, maxSourceLines int) (any, error) {
	snap, ranked, err := a.rankedSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return retrieval.SymbolContext(snap, ranked, query, retrieval.SymbolOptions{Kind: kind, File: file, MaxSourceLines: maxSourceLines, Calls: true, CallsLimit: 10})
}

// AgentStatus answers pan/status: the snapshot's shape.
func (a *AgentServeState) AgentStatus(ctx context.Context) (agent.StatusSummary, error) {
	snap, err := a.snapshot(ctx)
	if err != nil {
		return agent.StatusSummary{}, err
	}
	return agent.StatusSummary{
		Repository: snap.Root,
		Schema:     snap.SchemaVersion,
		Files:      len(snap.Files),
		Symbols:    len(snap.Symbols),
		Edges:      len(snap.Edges),
		Complete:   snap.Status.Complete,
		Limits:     snap.Status.Limits,
	}, nil
}

// AgentOverview answers pan/overview from the session snapshot.
func (a *AgentServeState) AgentOverview(ctx context.Context) (scan.OverviewReport, error) {
	snap, err := a.snapshot(ctx)
	if err != nil {
		return scan.OverviewReport{}, err
	}
	return scan.Overview(snap), nil
}

// AgentSymbols answers pan/symbols: case-insensitive substring matches
// capped by top (top <= 0 selects the protocol default bound).
func (a *AgentServeState) AgentSymbols(ctx context.Context, query string, top int) ([]analyze.Symbol, error) {
	snap, err := a.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if top <= 0 {
		top = agent.ServeSymbolsDefaultTop
	}
	if top > agent.ServeSymbolsMaxTop {
		top = agent.ServeSymbolsMaxTop
	}
	needle := strings.ToLower(query)
	var result []analyze.Symbol
	for _, symbol := range snap.Symbols {
		if query == "" || strings.Contains(strings.ToLower(symbol.Name), needle) {
			result = append(result, symbol)
			if len(result) == top {
				break
			}
		}
	}
	return result, nil
}

// AgentReport answers pan/report: the deterministic review document
// composed from the session snapshot.
func (a *AgentServeState) AgentReport(ctx context.Context) (review.Document, error) {
	snap, err := a.snapshot(ctx)
	if err != nil {
		return review.Document{}, err
	}
	report, err := composeReview(ctx, snap, 0)
	if err != nil {
		return review.Document{}, err
	}
	return review.NewDocument(0, report), nil
}
