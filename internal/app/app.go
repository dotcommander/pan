package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/cache"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/repo"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

// levelInfo is the diagnostic level for advisory notes surfaced inside a
// successful snapshot.
const levelInfo = "info"

// Deps carries the configuration-bound collaborators every service method
// shares.
type Deps struct {
	Config         config.Config
	SnapshotSource string
	CacheDir       string
}

// Service is the application layer: it builds bounded analysis snapshots
// and derives every command result from them.
type Service struct{ deps Deps }

// New constructs the application service from its dependencies.
func New(deps Deps) Service { return Service{deps: deps} }

// Snapshot builds one bounded analysis snapshot for root, including the
// discovered agent-instruction assets.
func (s Service) Snapshot(ctx context.Context, root string) (analyze.Snapshot, error) {
	return s.buildSnapshot(ctx, root, s.deps.Config)
}

// SnapshotWithMaxBytes applies a report-local source-read bound without
// mutating the service configuration or any target files.
func (s Service) SnapshotWithMaxBytes(ctx context.Context, root string, maxBytes int64) (analyze.Snapshot, error) {
	cfg := s.deps.Config
	if maxBytes > 0 {
		cfg.MaxFileBytes = maxBytes
	}
	return s.buildSnapshot(ctx, root, cfg)
}

func (s Service) buildSnapshot(ctx context.Context, root string, cfg config.Config) (analyze.Snapshot, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return analyze.Snapshot{}, fmt.Errorf("resolve repository root: %w", err)
	}
	var fallbackReason string
	if s.deps.SnapshotSource != "live" {
		base := s.deps.CacheDir
		if base == "" {
			base, err = cache.DefaultDir()
		}
		if err == nil {
			if cached, status, loadErr := cache.LoadValidated(ctx, absRoot, cache.DirFor(base, absRoot), cfg); loadErr == nil {
				return cached, nil
			} else {
				fallbackReason = status.Reason
			}
		} else {
			fallbackReason = "cache_directory_unavailable"
		}
	}
	snap, err := analyze.Build(ctx, absRoot, cfg)
	if err != nil {
		return analyze.Snapshot{}, err
	}
	snap.Instructions = capturedInstructions(snap, cfg.MaxInstructions)
	// Stamp every live snapshot with a deterministic identity and capture receipt.
	checked := time.Now().UTC()
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: cache.SnapshotID(cfg, snap, analyze.ManifestFromSnapshot(snap)), Source: "live", Freshness: "verified_at_start", CheckedAt: checked}
	if fallbackReason != "" {
		snap.Diagnostics = append(snap.Diagnostics, analyze.Diagnostic{Level: levelInfo, Message: "snapshot cache fallback: " + fallbackReason})
	}
	return snap, nil
}

func capturedInstructions(snap analyze.Snapshot, limit int) []string {
	var paths []string
	for _, file := range snap.Files {
		base := filepath.Base(file.Path)
		if base == "AGENTS.md" || base == "CLAUDE.md" {
			paths = append(paths, file.Path)
		}
	}
	slices.Sort(paths)
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	return paths
}

// Symbols returns the snapshot's symbols filtered by a case-insensitive
// name substring and capped by top; top <= 0 lists every match.
func (s Service) Symbols(ctx context.Context, root, query string, top int) (analyze.Snapshot, []analyze.Symbol, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	var result []analyze.Symbol
	for _, symbol := range snap.Symbols {
		if query == "" || strings.Contains(strings.ToLower(symbol.Name), strings.ToLower(query)) {
			result = append(result, symbol)
		}
	}
	if top > 0 && len(result) > top {
		result = result[:top]
	}
	return snap, result, nil
}

// Calls returns the snapshot's call edges touching selector by name, with
// depth reserved for future traversal bounds. An empty selector or a
// non-positive depth is a validation error.
func (s Service) Calls(ctx context.Context, root, selector string, depth int) (analyze.Snapshot, []analyze.Edge, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	if selector == "" {
		return snap, nil, errors.New("symbol selector is required")
	}
	var result []analyze.Edge
	for _, edge := range snap.Edges {
		if edge.Kind == "calls" && (edge.From == selector || edge.To == selector) {
			result = append(result, edge)
		}
	}
	if len(result) == 0 {
		snap.Diagnostics = append(snap.Diagnostics, analyze.Diagnostic{Level: levelInfo, Message: "no matching calls found in analyzed graph"})
	}
	if depth < 1 {
		return snap, nil, errors.New("depth must be positive")
	}
	return snap, result, nil
}

// defaultReviewChangeDays is the history window the composed review report
// summarizes when the caller does not choose one.
const defaultReviewChangeDays = 30

// ReviewOptions controls local report collection and deterministic selection.
// PatternFile is read locally and is never passed to a provider.
type ReviewOptions struct {
	Review   review.Options
	Days     int
	MaxBytes int64
	Patterns string
	Model    review.ModelOptions
}

// Overview derives repository summary counts from one bounded snapshot.
func (s Service) Overview(ctx context.Context, root string) (analyze.Snapshot, scan.OverviewReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.OverviewReport{}, err
	}
	return snap, scan.Overview(snap), nil
}

// Risks derives the deterministic risk-ranked review queue. top > 0 caps
// the returned file list; lanes always reflect every scored file.
func (s Service) Risks(ctx context.Context, root string, top int) (analyze.Snapshot, scan.RiskReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.RiskReport{}, err
	}
	risks, err := scan.Risk(ctx, snap, top)
	if err != nil {
		return analyze.Snapshot{}, scan.RiskReport{}, err
	}
	return snap, risks, nil
}

// Surface extracts the public-surface inventory from bounded Go source reads.
func (s Service) Surface(ctx context.Context, root string, top int) (analyze.Snapshot, scan.SurfaceReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.SurfaceReport{}, err
	}
	surface, err := scan.Surface(ctx, snap, top)
	if err != nil {
		return analyze.Snapshot{}, scan.SurfaceReport{}, err
	}
	return snap, surface, nil
}

// Effects extracts side-effect and trust-boundary leads from bounded Go
// source reads.
func (s Service) Effects(ctx context.Context, root string, top int) (analyze.Snapshot, scan.EffectsReport, error) {
	return s.EffectsWithOptions(ctx, root, scan.EffectsOptions{Limit: top})
}

// EffectsWithOptions extracts selected side-effect and trust-boundary leads
// from one bounded snapshot.
func (s Service) EffectsWithOptions(ctx context.Context, root string, options scan.EffectsOptions) (analyze.Snapshot, scan.EffectsReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.EffectsReport{}, err
	}
	effects, err := scan.EffectsWithOptions(ctx, snap, options)
	if err != nil {
		return analyze.Snapshot{}, scan.EffectsReport{}, err
	}
	scoreEffects(&effects, snap)
	return snap, effects, nil
}

// Hygiene inspects git worktree drift, degrading to snapshot-derived counts
// with an explicit note when git evidence is unavailable.
func (s Service) Hygiene(ctx context.Context, root string) (analyze.Snapshot, scan.HygieneReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.HygieneReport{}, err
	}
	return snap, scan.Hygiene(ctx, snap.Root, snap), nil
}

// Changes summarizes per-file churn over a bounded git history window,
// degrading to an explicit note when git history is unavailable.
func (s Service) Changes(ctx context.Context, root string, days, top int) (analyze.Snapshot, scan.ChangesReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.ChangesReport{}, err
	}
	return snap, scan.Changes(ctx, snap.Root, days, top, time.Time{}), nil
}

// ReviewReport composes every scan packet into one deterministic audit
// report with a merged read queue. top > 0 caps the queue.
func (s Service) ReviewReport(ctx context.Context, root string, top int) (analyze.Snapshot, review.Report, error) {
	return s.ReviewReportWithOptions(ctx, root, ReviewOptions{Review: review.Options{Top: top}})
}

// ReviewReportWithOptions composes scan packets and then applies local report
// selection. It only calls a configured model scorer after deterministic evidence is assembled.
func (s Service) ReviewReportWithOptions(ctx context.Context, root string, options ReviewOptions) (analyze.Snapshot, review.Report, error) {
	if options.Days <= 0 {
		options.Days = defaultReviewChangeDays
	}
	if err := options.Review.Validate(); err != nil {
		return analyze.Snapshot{}, review.Report{}, err
	}
	snap, err := s.SnapshotWithMaxBytes(ctx, root, options.MaxBytes)
	if err != nil {
		return analyze.Snapshot{}, review.Report{}, err
	}
	if options.Model.Model != "" || options.Model.Local {
		options.Model.ContentHashes = review.ContentHashes(snap.Root, snap.Files)
	}
	var patterns *scan.CustomPatterns
	if options.Patterns != "" {
		catalog, catalogErr := scan.LoadCustomPatternsFile(options.Patterns)
		if catalogErr != nil {
			return analyze.Snapshot{}, review.Report{}, catalogErr
		}
		patterns = &catalog
	}
	risks, err := scan.RiskWithCustomPatterns(ctx, snap, 0, patterns)
	if err != nil {
		return analyze.Snapshot{}, review.Report{}, err
	}
	surface, err := scan.Surface(ctx, snap, 0)
	if err != nil {
		return analyze.Snapshot{}, review.Report{}, err
	}
	effects, err := scan.Effects(ctx, snap, 0)
	if err != nil {
		return analyze.Snapshot{}, review.Report{}, err
	}
	hygiene := scan.Hygiene(ctx, snap.Root, snap)
	changes := scan.Changes(ctx, snap.Root, options.Days, 0, time.Time{})
	report := review.Compose(review.Packets{Overview: scan.Overview(snap), Risks: risks, Surface: surface, Effects: effects, Hygiene: hygiene, Changes: changes, Paths: review.ReportPaths(snap.Root, snap.Files)}, 0)
	report, err = options.Model.Score(ctx, report)
	if err != nil {
		return analyze.Snapshot{}, review.Report{}, err
	}
	report, err = review.ApplyOptions(report, options.Review)
	if err != nil {
		return analyze.Snapshot{}, review.Report{}, err
	}
	return snap, report, nil
}

// ReviewDocument composes the deterministic audit report and projects it
// into the versioned machine document shared by `review report --json` and
// the agent report operation.
func (s Service) ReviewDocument(ctx context.Context, root string, top int) (analyze.Snapshot, review.Document, error) {
	snap, report, err := s.ReviewReport(ctx, root, top)
	if err != nil {
		return analyze.Snapshot{}, review.Document{}, err
	}
	return snap, review.NewDocument(top, report), nil
}

// Doctor runs one bounded analysis pass and derives the deterministic
// analyzer health report. It performs local checks only.
func (s Service) Doctor(ctx context.Context, root string) (analyze.Snapshot, scan.DoctorReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.DoctorReport{}, err
	}
	source, path := config.State()
	return snap, scan.Doctor(ctx, snap.Root, snap, source, path), nil
}

func scoreEffects(report *scan.EffectsReport, snap analyze.Snapshot) {
	ranked := ranking.Rank(snap, repo.ModulePath(snap.Root), ranking.Options{})
	scores := make(map[string]int, len(ranked))
	for _, file := range ranked {
		scores[file.Path] = file.Score
	}
	scan.ApplyEffectScores(report, scores)
}
