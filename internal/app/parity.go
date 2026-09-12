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
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/fileclass"
	"github.com/dotcommander/pan/internal/lsp"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/repo"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

// Orphans returns bounded unused-export candidates, preferring configured
// language-server reference evidence when it is available.
func (s Service) Orphans(ctx context.Context, root string, top int) (analyze.Snapshot, scan.OrphanReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.OrphanReport{}, err
	}
	service := lsp.NewService(s.deps.Config.Lsp, s.deps.Config.Exclude)
	report, err := scan.OrphansWithReferences(ctx, snap, top, func(ctx context.Context, candidate scan.OrphanCandidate) (scan.ReferenceResult, error) {
		references, referenceErr := service.References(ctx, snap.Root, filepath.Join(snap.Root, filepath.FromSlash(candidate.File)), candidate.Line, candidate.Name)
		if referenceErr != nil {
			return scan.ReferenceResult{}, referenceErr
		}
		result := scan.ReferenceResult{Available: references.Result.Status == "ok"}
		for _, location := range references.Result.Locations {
			if filepath.Clean(location.Path) == filepath.Clean(filepath.Join(snap.Root, filepath.FromSlash(candidate.File))) && location.Range.Start.Line+1 == candidate.Line {
				continue
			}
			if strings.HasSuffix(location.Path, "_test.go") {
				result.Test++
			} else {
				result.NonTest++
			}
		}
		return result, nil
	})
	return snap, report, err
}

// Inventory returns lexical ownership evidence for one supported boundary.
func (s Service) Inventory(ctx context.Context, root, boundary string, top int) (analyze.Snapshot, scan.InventoryReport, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, scan.InventoryReport{}, err
	}
	report, err := scan.Inventory(ctx, snap, boundary, top)
	return snap, report, err
}

// FlowGraph returns deterministic import edges, optionally restricted to a package substring.
func (s Service) FlowGraph(ctx context.Context, root, pkg string) (analyze.Snapshot, []analyze.Edge, error) {
	snap, err := s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	out := make([]analyze.Edge, 0)
	for _, edge := range snap.Edges {
		if edge.Kind == "imports" && (pkg == "" || edge.From == pkg || edge.To == pkg) {
			out = append(out, edge)
		}
	}
	slices.SortFunc(out, func(a, b analyze.Edge) int {
		if a.From != b.From {
			if a.From < b.From {
				return -1
			}
			return 1
		}
		if a.To < b.To {
			return -1
		}
		if a.To > b.To {
			return 1
		}
		return 0
	})
	return snap, out, nil
}

// AuditOptions selects deterministic review packets. Language is validated
// against the shared effects vocabulary because every audit packet uses the
// same parsed-source language IDs. Intent ranks the admitted files before a
// packet applies its own domain scoring.
type AuditOptions struct {
	Limit              int
	Language           string
	Intent             string
	IncludeClasses     []string
	HistoryWindow      int
	RefactorSignatures bool
}

type reviewBriefHygiene struct {
	Counts        scan.HygieneCounts `json:"counts"`
	UntrackedCode []string           `json:"untracked_code,omitempty"`
	Details       []string           `json:"details_command,omitempty"`
	Note          string             `json:"note,omitempty"`
}

// Validate rejects invalid audit selectors before a snapshot is built.
func (o AuditOptions) Validate() error {
	if o.Limit < 0 {
		return errors.New("audit limit must not be negative")
	}
	if o.HistoryWindow < 0 {
		return errors.New("history window must not be negative")
	}
	for _, class := range o.IncludeClasses {
		if !fileclass.Valid(class) {
			return fmt.Errorf("unknown file class %q", class)
		}
	}
	return (scan.EffectsOptions{Language: o.Language}).Validate()
}

func (s Service) auditSnapshot(ctx context.Context, root string, options AuditOptions) (original, filtered analyze.Snapshot, err error) {
	err = options.Validate()
	if err != nil {
		return analyze.Snapshot{}, analyze.Snapshot{}, err
	}
	original, err = s.Snapshot(ctx, root)
	if err != nil {
		return analyze.Snapshot{}, analyze.Snapshot{}, err
	}
	filtered = filterAuditSnapshot(original, options)
	return original, filtered, nil
}

func filterAuditSnapshot(snap analyze.Snapshot, options AuditOptions) analyze.Snapshot {
	ranked := ranking.Rank(snap, repo.ModulePath(snap.Root), ranking.Options{Intent: options.Intent})
	byPath := make(map[string]analyze.File, len(snap.Files))
	for _, file := range snap.Files {
		byPath[file.Path] = file
	}
	filtered := snap
	filtered.Files = make([]analyze.File, 0, len(snap.Files))
	for _, item := range ranked {
		if options.Language != "" && item.Language != options.Language {
			continue
		}
		filtered.Files = append(filtered.Files, byPath[item.Path])
	}
	return filtered
}

// RisksWithOptions derives the deterministic risk-ranked queue after applying
// shared audit selectors.
func (s Service) RisksWithOptions(ctx context.Context, root string, options AuditOptions) (analyze.Snapshot, scan.RiskReport, error) {
	snap, filtered, err := s.auditSnapshot(ctx, root, options)
	if err != nil {
		return analyze.Snapshot{}, scan.RiskReport{}, err
	}
	report, err := scan.RiskWithOptions(ctx, filtered, options.Limit, scan.RiskOptions{IncludeClasses: options.IncludeClasses})
	return snap, report, err
}

// SurfaceWithOptions extracts public-surface evidence after shared selectors.
func (s Service) SurfaceWithOptions(ctx context.Context, root string, options AuditOptions) (analyze.Snapshot, scan.SurfaceReport, error) {
	snap, filtered, err := s.auditSnapshot(ctx, root, options)
	if err != nil {
		return analyze.Snapshot{}, scan.SurfaceReport{}, err
	}
	report, err := scan.Surface(ctx, filtered, options.Limit)
	return snap, report, err
}

// EffectsWithAuditOptions extracts side-effect evidence after shared selectors.
func (s Service) EffectsWithAuditOptions(ctx context.Context, root string, options AuditOptions, kind string) (analyze.Snapshot, scan.EffectsReport, error) {
	snap, filtered, err := s.auditSnapshot(ctx, root, options)
	if err != nil {
		return analyze.Snapshot{}, scan.EffectsReport{}, err
	}
	report, err := scan.EffectsWithOptions(ctx, filtered, scan.EffectsOptions{Limit: options.Limit, Kind: kind, Language: options.Language})
	if err == nil {
		scoreEffects(&report, filtered)
	}
	return snap, report, err
}

// ReviewBrief builds the bounded first-read portion of the audit report.
func (s Service) ReviewBrief(ctx context.Context, root string, top int) (analyze.Snapshot, map[string]any, error) {
	return s.ReviewBriefWithOptions(ctx, root, AuditOptions{Limit: top})
}

// ReviewBriefWithOptions composes selected first-read evidence, optional
// bounded history, and exact signature-risk evidence in one local response.
func (s Service) ReviewBriefWithOptions(ctx context.Context, root string, options AuditOptions) (analyze.Snapshot, map[string]any, error) {
	if err := options.Validate(); err != nil {
		return analyze.Snapshot{}, nil, err
	}
	// Workspace artifacts remain visible through the hygiene packet, but must
	// not consume source-analysis bounds or displace repository-owned code.
	cfg := s.deps.Config.Normalized()
	ignored, ignoredErr := scan.IgnoredPaths(ctx, root)
	// .work is Pan's conventional artifact root. Excluding the directory as one
	// boundary avoids emitting a skipped row for every ignored descendant.
	ignored = append(ignored, ".work")
	if ignoredErr != nil {
		ignored = []string{".work"}
	}
	cfg.Exclude = config.NormalizeExcludes(append(cfg.Exclude, ignored...))
	snap, err := s.buildSnapshot(ctx, root, cfg)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	filtered := filterAuditSnapshot(snap, options)
	hygiene := scan.Hygiene(ctx, snap.Root, snap)
	risks, err := scan.Risk(ctx, filtered, options.Limit)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	surface, err := scan.Surface(ctx, filtered, options.Limit)
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	effects, err := scan.EffectsWithOptions(ctx, filtered, scan.EffectsOptions{Limit: options.Limit, Language: options.Language})
	if err != nil {
		return analyze.Snapshot{}, nil, err
	}
	report := review.Compose(review.Packets{Overview: scan.Overview(filtered), Risks: risks, Surface: surface, Effects: effects, Changes: scan.Changes(ctx, snap.Root, defaultReviewChangeDays, options.Limit, time.Time{}), Paths: review.ReportPaths(snap.Root, filtered.Files)}, options.Limit)
	briefHygiene := reviewBriefHygiene{Counts: hygiene.Counts, UntrackedCode: hygiene.UntrackedCode, Note: hygiene.Note}
	if hygiene.Counts.IgnoredSource > 0 {
		briefHygiene.Details = []string{"pan", "scan", "hygiene", "--repo", "."}
	}
	result := map[string]any{"overview": report.Overview, "read_queue": report.ReadQueue, "workspace_hygiene": briefHygiene, "review_gates": []string{"go test ./...", "go vet ./..."}, "caveat": "Deterministic local evidence; inspect ranked files before changing them."}
	if options.HistoryWindow > 0 {
		history, err := scan.AuditHistory(ctx, snap.Root, options.HistoryWindow)
		if err != nil {
			return analyze.Snapshot{}, nil, err
		}
		result["history"] = history
	}
	if options.RefactorSignatures {
		refactors, err := scan.RefactorSignatures(ctx, snap)
		if err != nil {
			return analyze.Snapshot{}, nil, err
		}
		result["refactor_signatures"] = refactors
	}
	return snap, result, nil
}
