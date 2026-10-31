package app

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
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
	ranked      []ranking.RankedFile
	sources     map[string]agentSource
	redacted    map[string]bool
	hashes      map[string]string
	stamps      map[string]analyze.FileStamp
	fingerprint string
	builtAt     time.Time
	verifiedAt  time.Time
	rebuilds    int
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
	return a.snapshotLocked(ctx)
}

// snapshotLocked requires a.mu; ranking and source state share one generation.
func (a *AgentServeState) snapshotLocked(ctx context.Context) (analyze.Snapshot, error) {
	if a.ready && a.err == nil && a.sourcesFresh(ctx) {
		a.verifiedAt = time.Now().UTC()
		return a.snap, nil
	}
	a.rebuilds++
	a.ready = false
	a.ranked = nil
	a.snap, a.err = a.service.Snapshot(ctx, a.root)
	if a.err == nil {
		a.err = a.captureSources(ctx, a.snap)
	}
	if a.err == nil {
		a.builtAt = time.Now().UTC()
		a.verifiedAt = a.builtAt
		a.ready = true
	}
	return a.snap, a.err
}

// sessionFacts returns the rebuild counter and evidence timestamps for
// snapshot/status under the state lock.
func (a *AgentServeState) sessionFacts() (rebuilds int, builtAt, verifiedAt time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rebuilds, a.builtAt, a.verifiedAt
}

func (a *AgentServeState) rankedSnapshot(ctx context.Context) (analyze.Snapshot, []ranking.RankedFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap, err := a.snapshotLocked(ctx)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	if a.ranked == nil {
		a.ranked = ranking.Rank(snap, repo.ModulePath(snap.Root), ranking.Options{})
	}
	// Map rendering filters and assigns detail levels in place. Do not expose
	// cached slice, maps or nested slices to any request's mutable consumer.
	return snap, cloneAgentRanking(a.ranked), nil
}

func cloneAgentRanking(ranked []ranking.RankedFile) []ranking.RankedFile {
	out := slices.Clone(ranked)
	for i := range out {
		out[i].Symbols = slices.Clone(out[i].Symbols)
		out[i].Imports = slices.Clone(out[i].Imports)
		out[i].InternalImports = slices.Clone(out[i].InternalImports)
		out[i].Components = maps.Clone(out[i].Components)
	}
	return out
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
	result := codemap.Build(ranked, codemap.Options{Mode: mode, Root: snap.Root, IncludeTests: true, Captured: snap.Captured})
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
	_, builtAt, _ := a.sessionFacts()
	built := ""
	if !builtAt.IsZero() {
		built = builtAt.Format(time.RFC3339)
	}
	return agent.MapStatus{BuiltAt: built, Root: snap.Root}, nil
}

// AgentSymbolFind returns the complete symbol/find result against current
// evidence: the compatibility matches envelope for hits, or the explanation
// report with outcome and guidance for a miss. The transport must not wrap
// the report again.
func (a *AgentServeState) AgentSymbolFind(ctx context.Context, query string) (any, error) {
	snap, ranked, err := a.rankedSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	parsed := retrieval.ParseFindQuery(query)
	matches := retrieval.Find(ranked, parsed.Name, parsed.Kind, parsed.File)
	if len(matches) > 0 {
		return map[string]any{"matches": matches}, nil
	}
	return retrieval.NewFindReportWithStatus(parsed, matches, snap.Status), nil
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

// AgentSnapshotStatus answers snapshot/status: evidence freshness,
// coverage, and session reuse for the served repository. Every call
// re-verifies freshness first, so a successful result is current as of
// VerifiedAt.
func (a *AgentServeState) AgentSnapshotStatus(ctx context.Context) (agent.SnapshotStatus, error) {
	snap, err := a.snapshot(ctx)
	if err != nil {
		return agent.SnapshotStatus{}, err
	}
	rebuilds, builtAt, verifiedAt := a.sessionFacts()
	status := agent.SnapshotStatus{
		Ready:        true,
		Root:         snap.Root,
		Schema:       snap.SchemaVersion,
		Rebuilds:     rebuilds,
		Complete:     snap.Status.Complete,
		Limits:       snap.Status.Limits,
		SkippedCount: snap.Status.SkippedCount,
	}
	if !builtAt.IsZero() {
		status.BuiltAt = builtAt.Format(time.RFC3339)
	}
	if !verifiedAt.IsZero() {
		status.VerifiedAt = verifiedAt.Format(time.RFC3339)
	}
	if info := snap.Status.Snapshot; info != nil {
		status.SnapshotID = info.ID
		status.Source = info.Source
	}
	return status, nil
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
