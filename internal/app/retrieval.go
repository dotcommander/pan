package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/cache"
	"github.com/dotcommander/pan/internal/codemap"
	"github.com/dotcommander/pan/internal/fileclass"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/repo"
	"github.com/dotcommander/pan/internal/retrieval"
)

// ranked builds one snapshot plus its ranking. The module path resolves Go
// import edges to repository directories; without a go.mod, ranking falls
// back to suffix matching.
func (s Service) ranked(ctx context.Context, root string, opts ranking.Options) (analyze.Snapshot, []ranking.RankedFile, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	ranked := ranking.Rank(snap, repo.ModulePath(snap.Root), opts)
	return snap, ranked, nil
}

// BriefEntry is one ranked file in the brief orientation list.
type BriefEntry struct {
	Path        string         `json:"path"`
	Score       int            `json:"score"`
	Tag         string         `json:"tag,omitempty"`
	Class       string         `json:"class,omitempty"`
	Components  map[string]int `json:"components,omitempty"`
	Why         []string       `json:"why,omitempty"`
	CallerCount int            `json:"caller_count,omitempty"`
	Tested      bool           `json:"tested,omitempty"`
	IntentHits  int            `json:"intent_hits,omitempty"`
	Confidence  string         `json:"confidence,omitempty"`
	Families    map[string]int `json:"families,omitempty"`
}

// BriefResult carries the shared snapshot and rendered orientation data for a
// task-oriented repository brief.
type BriefResult struct {
	Snapshot     analyze.Snapshot
	Entries      []BriefEntry
	Map          codemap.Result
	Coverage     BriefCoverage
	NextCommands []BriefNextCommand `json:"next_commands,omitempty"`
	Budget       BriefBudget
}

type BriefNextCommand struct {
	Args   []string `json:"args"`
	Reason string   `json:"reason"`
}

type BriefCoverage struct {
	AnalyzedFiles int            `json:"analyzed_files"`
	TotalFiles    *int           `json:"total_files"`
	Complete      bool           `json:"complete"`
	Limits        []string       `json:"limits,omitempty"`
	Skipped       []string       `json:"skipped,omitempty"`
	SkippedCount  int            `json:"skipped_count"`
	Classes       map[string]int `json:"classes,omitempty"`
	Bounds        BriefBounds    `json:"bounds"`
}

type BriefBounds struct {
	MaxFiles      int   `json:"max_files"`
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	MaxNodes      int   `json:"max_nodes"`
}

type BriefBudget struct {
	Unit        string               `json:"unit"`
	Requested   int                  `json:"requested"`
	Used        int                  `json:"used"`
	Scope       string               `json:"scope"`
	Truncations []analyze.Truncation `json:"truncations,omitempty"`
}

// Brief returns the task-oriented summary: instruction assets, file and
// symbol inventories, and the top ranked files oriented by intent.
func (s Service) Brief(ctx context.Context, root, intent string, budget int) (BriefResult, error) {
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{Intent: intent})
	if err != nil {
		return BriefResult{}, err
	}
	production := productionRanked(ranked)
	selected := production[:min(20, len(production))]
	entries := make([]BriefEntry, 0, len(selected))
	commands := make([]BriefNextCommand, 0, 1)
	classes := make(map[string]int)
	for _, rf := range ranked {
		classes[string(rf.Class)]++
	}
	for _, rf := range selected {
		entries = append(entries, BriefEntry{Path: rf.Path, Score: rf.Score, Tag: rf.Tag,
			Class: string(rf.Class), Components: rf.Components, Why: briefWhy(rf.Components), CallerCount: rf.CallerCount,
			Tested: rf.Tested, IntentHits: rf.IntentHits, Confidence: rf.Confidence,
			Families: codemap.SymbolFamilies(rf.Symbols)})
		if len(commands) == 0 && rf.CallerCount >= 2 && len(rf.Symbols) > 0 {
			commands = append(commands, BriefNextCommand{Args: []string{"flow", "calls", rf.Symbols[0].Name, "--depth", "2"}, Reason: "trace callers of a highly connected symbol"})
		}
	}
	cfg := s.deps.Config.Normalized()
	coverage := BriefCoverage{
		AnalyzedFiles: len(snap.Files), TotalFiles: knownTotalFiles(snap),
		Complete: snap.Status.Complete, Limits: snap.Status.Limits, Skipped: snap.Status.Skipped,
		SkippedCount: snap.Status.SkippedCount, Classes: classes,
		Bounds: BriefBounds{MaxFiles: cfg.MaxFiles, MaxFileBytes: cfg.MaxFileBytes, MaxTotalBytes: cfg.MaxTotalBytes, MaxNodes: cfg.MaxNodes},
	}
	encoded, _ := json.Marshal(struct {
		Instructions []string     `json:"instructions"`
		Entries      []BriefEntry `json:"top_files"`
	}{snap.Instructions, entries})
	return BriefResult{
		Snapshot:     snap,
		Entries:      entries,
		Map:          codemap.Build(selected, codemap.Options{Mode: codemap.ModeEnriched, Tokens: budget, Root: snap.Root, Edges: snap.Edges}),
		Coverage:     coverage,
		NextCommands: commands,
		Budget:       BriefBudget{Unit: "bytes", Requested: budget, Used: len(encoded), Scope: "instructions and top_files"},
	}, nil
}

func knownTotalFiles(snap analyze.Snapshot) *int {
	if !snap.Status.Complete {
		return nil
	}
	total := len(snap.Files)
	return &total
}

// productionRanked is the single brief selection boundary. Entries and the
// brief map must describe the same production-only ranked slice; otherwise an
// agent can see a file in one surface and miss it in the other.
func productionRanked(ranked []ranking.RankedFile) []ranking.RankedFile {
	production := make([]ranking.RankedFile, 0, len(ranked))
	for _, file := range ranked {
		if file.Class == fileclass.Production {
			production = append(production, file)
		}
	}
	return production
}

func briefWhy(components map[string]int) []string {
	type component struct {
		name  string
		score int
	}
	items := make([]component, 0, len(components))
	for name, score := range components {
		if score != 0 {
			items = append(items, component{name, score})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].name < items[j].name
	})
	if len(items) > 3 {
		items = items[:3]
	}
	labels := map[string]string{
		ranking.ComponentEntry:       "application entry point",
		ranking.ComponentOrientation: "repository orientation file",
		ranking.ComponentSymbols:     "defines structural symbols",
		ranking.ComponentDepth:       "near the repository root",
		ranking.ComponentImports:     "widely imported owner",
		ranking.ComponentCallers:     "called from multiple locations",
		ranking.ComponentTests:       "has nearby test evidence",
		ranking.ComponentTestDemote:  "test-only file",
		ranking.ComponentIntent:      "matches the requested intent",
		ranking.ComponentConsumed:    "already selected by the agent",
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = labels[item.name]
		if out[i] == "" {
			out[i] = fmt.Sprintf("ranking signal %s", item.name)
		}
	}
	return out
}

// Map renders the ranked, token-budgeted repository map.
func (s Service) Map(ctx context.Context, root string, opts codemap.Options) (analyze.Snapshot, codemap.Result, error) {
	snap, ranked, opts, err := s.mapInputs(ctx, root, opts)
	if err != nil {
		return analyze.Snapshot{}, codemap.Result{}, err
	}
	return snap, codemap.Build(ranked, opts), nil
}

// MapStructured returns the Pan-compatible structured map for the same
// ranking and option flow as Map.
func (s Service) MapStructured(ctx context.Context, root string, opts codemap.Options) (codemap.StructuredOutput, error) {
	snap, ranked, opts, err := s.mapInputs(ctx, root, opts)
	if err != nil {
		return codemap.StructuredOutput{}, err
	}
	return codemap.BuildStructured(snap, ranked, opts), nil
}

func (s Service) mapInputs(ctx context.Context, root string, opts codemap.Options) (analyze.Snapshot, []ranking.RankedFile, codemap.Options, error) {
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{Intent: opts.Intent, Consumed: opts.Consumed, IncludeTests: opts.IncludeTests})
	if err != nil {
		return analyze.Snapshot{}, nil, codemap.Options{}, err
	}
	opts.Root, opts.Edges = snap.Root, snap.Edges
	if opts.SymbolRefs {
		ranking.ApplySymbolReferenceBonus(snap.Root, ranked)
	}
	if opts.Calls {
		ranking.ApplyCallEdgeBonus(ranked, snap.Edges, opts.CallsThreshold, opts.CallsIncludeTests)
	}
	return snap, ranked, opts, nil
}

// Find resolves one symbol query against the ranked symbol set.
func (s Service) Find(ctx context.Context, root, query, kind, file string) (analyze.Snapshot, []retrieval.SymbolMatch, error) {
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{})
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	parsed := retrieval.ParseFindQuery(query)
	if kind == "" {
		kind = parsed.Kind
	}
	if file == "" {
		file = parsed.File
	}
	matches := retrieval.Find(ranked, parsed.Name, kind, file)
	if len(matches) == 0 {
		if parsed.Name == "" {
			return snap, nil, errors.New("symbol query is required")
		}
		snap.Diagnostics = append(snap.Diagnostics, analyze.Diagnostic{
			Level:   levelInfo,
			Message: fmt.Sprintf("no symbols matched %q", query),
		})
	}
	return snap, matches, nil
}

// SymbolContext resolves one symbol query into its bounded context bundle.
func (s Service) SymbolContext(ctx context.Context, root, query string, opts retrieval.SymbolOptions) (analyze.Snapshot, retrieval.SymbolResult, error) {
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{})
	if err != nil {
		return analyze.Snapshot{}, retrieval.SymbolResult{}, err
	}
	result, err := retrieval.SymbolContext(snap, ranked, query, opts)
	if err != nil {
		return snap, result, err
	}
	return snap, result, nil
}

// Explain reports why one file ranked and rendered the way it did.
func (s Service) Explain(ctx context.Context, root, file string, tokens int) (analyze.Snapshot, retrieval.ExplainResult, error) {
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{})
	if err != nil {
		return analyze.Snapshot{}, retrieval.ExplainResult{}, err
	}
	result, err := retrieval.Explain(ranked, file, tokens)
	if err != nil {
		return snap, result, err
	}
	return snap, result, nil
}

// Endpoint resolves one route query into its vertical-slice bundle.
func (s Service) Endpoint(ctx context.Context, root, route string) (analyze.Snapshot, retrieval.EndpointContext, error) {
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{})
	if err != nil {
		return analyze.Snapshot{}, retrieval.EndpointContext{}, err
	}
	result, err := retrieval.Endpoint(ctx, snap, ranked, route)
	if err != nil {
		return snap, result, err
	}
	return snap, result, nil
}

// Routes lists HTTP route registrations without resolving a handler slice.
func (s Service) Routes(ctx context.Context, root string) (analyze.Snapshot, []retrieval.RouteRegistration, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	routes, err := retrieval.Routes(ctx, snap)
	return snap, routes, err
}

// Task builds the bounded goal-oriented context packet.
func (s Service) Task(ctx context.Context, root, goal string, opts retrieval.TaskOptions) (analyze.Snapshot, retrieval.TaskReport, error) {
	consumed, err := retrieval.NormalizeConsumed(absPath(root), opts.Consumed)
	if err != nil {
		return analyze.Snapshot{}, retrieval.TaskReport{}, err
	}
	opts.Consumed = consumed
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{Intent: goal, Consumed: consumed})
	if err != nil {
		return analyze.Snapshot{}, retrieval.TaskReport{}, err
	}
	report, err := retrieval.Task(snap, ranked, goal, opts)
	if err != nil {
		return snap, report, err
	}
	return snap, report, nil
}

// ImpactReport bundles the blast-radius answer for one selector: the
// lexical call edges touching the selector by name, the impact facts for
// the file defining the best match, and whether any symbol matched at all.
type ImpactReport struct {
	Edges  []analyze.Edge
	Impact retrieval.ImpactResult
	Found  bool
}

// Impact summarizes the blast radius of the file defining the best match for
// selector, alongside the call edges touching the selector by name. Found
// reports whether any symbol matched; call evidence is always lexical.
func (s Service) Impact(ctx context.Context, root, selector string) (analyze.Snapshot, ImpactReport, error) {
	if selector == "" {
		return analyze.Snapshot{}, ImpactReport{}, errors.New("symbol selector is required")
	}
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, ImpactReport{}, err
	}
	report := ImpactReport{Edges: callEdges(snap, selector)}
	if len(report.Edges) == 0 {
		snap.Diagnostics = append(snap.Diagnostics, analyze.Diagnostic{Level: levelInfo, Message: "no matching calls found in analyzed graph"})
	}
	ranked := ranking.Rank(snap, repo.ModulePath(snap.Root), ranking.Options{})
	matches := retrieval.Find(ranked, selector, "", "")
	if len(matches) == 0 {
		return snap, report, nil
	}
	report.Impact, _ = retrieval.Impact(ranked, matches[0].File)
	report.Found = true
	return snap, report, nil
}

// callEdges selects call edges touching selector by name, preserving the
// snapshot's order.
func callEdges(snap analyze.Snapshot, selector string) []analyze.Edge {
	var edges []analyze.Edge
	for _, edge := range snap.Edges {
		if edge.Kind == "calls" && (edge.From == selector || edge.To == selector) {
			edges = append(edges, edge)
		}
	}
	return edges
}

// CacheStatus reports cache freshness for root under dir. An empty dir uses
// the default user cache location.
func (s Service) CacheStatus(ctx context.Context, root, dir string) (cache.Status, error) {
	root, dir, err := s.cachePaths(root, dir)
	if err != nil {
		return cache.Status{}, err
	}
	return cache.Inspect(ctx, root, dir, s.deps.Config), nil
}

// CacheWarm builds one bounded analysis pass for root and stores it under
// dir. The returned status must be fresh and usable; any store or build
// failure is an error.
func (s Service) CacheWarm(ctx context.Context, root, dir string) (analyze.Snapshot, cache.Status, error) {
	root, dir, err := s.cachePaths(root, dir)
	if err != nil {
		return analyze.Snapshot{}, cache.Status{}, err
	}
	live := s
	live.deps.SnapshotSource = "live"
	snap, err := live.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, cache.Status{}, err
	}
	stamps := snap.Stamps()
	if _, err := cache.Store(dir, root, s.deps.Config, snap, stamps); err != nil {
		return snap, cache.Status{}, err
	}
	status := cache.Inspect(ctx, root, dir, s.deps.Config)
	if !status.Usable || status.Stale {
		return snap, status, fmt.Errorf("cache warm did not produce a fresh usable cache: %s", status.Reason)
	}
	return snap, status, nil
}

// CacheClear removes the pan-owned cache entry under dir.
func (s Service) CacheClear(root, dir string) (cache.ClearResult, error) {
	_, dir, err := s.cachePaths(root, dir)
	if err != nil {
		return cache.ClearResult{}, err
	}
	return cache.Clear(dir)
}

// cachePaths resolves the absolute repository root and effective cache
// directory for one cache command.
func (s Service) cachePaths(root, dir string) (cacheRoot, entryDir string, err error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve repository root: %w", err)
	}
	if dir == "" {
		dir, err = cache.DefaultDir()
		if err != nil {
			return "", "", err
		}
	}
	return abs, cache.DirFor(dir, abs), nil
}

func absPath(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}
