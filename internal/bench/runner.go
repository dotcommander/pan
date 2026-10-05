package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/retrieval"
)

// ReportSchema is stamped on every benchmark report.
const ReportSchema = "pan.retrieval-bench/v1"

// Bench defaults and bounds.
const (
	DefaultTokenBudget = 4096
	DefaultTopRows     = 50
	// maxArchiveBytes caps one `git archive` stream so an oversized mirror
	// fails the instance instead of exhausting memory or disk.
	maxArchiveBytes = 1 << 30
	// maxPathList caps the ranked-path lists carried in report rows.
	maxPathList = 20
)

// System names in the report.
const (
	SystemPan  = "pan"
	SystemBM25 = "bm25"
)

// SnapshotSource supplies bounded analysis snapshots for one root.
type SnapshotSource interface {
	Snapshot(ctx context.Context, root string) (analyze.Snapshot, error)
}

// RunOptions controls one benchmark run.
type RunOptions struct {
	// Dataset is the SWE-bench-style JSONL path; Mirrors is the directory
	// holding org__name git mirrors.
	Dataset string
	Mirrors string
	// Work is the root for per-instance checkouts. Each instance gets
	// Work/<sanitized instance id>; it is recreated and removed per run.
	Work string
	// RepoFilters keeps only exact org/name instances when non-empty.
	RepoFilters []string
	// Limit caps scored instances; zero scores all matching instances.
	Limit int
	// TopRows caps per-instance detail rows in the report; zero lists all.
	TopRows int
	// TokenBudget is the per-case retrieval budget.
	TokenBudget int
	// Policy selects the retrieval policy id; empty uses the default.
	Policy string
}

// SkippedInstance records one instance the run could not score and why.
type SkippedInstance struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// SystemScore aggregates one system's metrics over the scored instances.
type SystemScore struct {
	System  string  `json:"system"`
	Cases   int     `json:"cases"`
	Metrics Metrics `json:"metrics"`
}

// BenchRow is one scored instance: gold paths and both systems' ranked
// paths with their metrics.
type BenchRow struct {
	InstanceID       string   `json:"instance_id"`
	Repo             string   `json:"repo"`
	GoldPaths        []string `json:"gold_paths"`
	PanPaths         []string `json:"pan_paths,omitempty"`
	BM25Paths        []string `json:"bm25_paths,omitempty"`
	RequestTruncated bool     `json:"request_truncated,omitempty"`
	Pan              Metrics  `json:"pan"`
	BM25             Metrics  `json:"bm25"`
}

// BenchReport is the deterministic benchmark document. It carries no
// timestamps: an unchanged dataset, mirror, and analyzer produce an
// identical report.
type BenchReport struct {
	Schema           string               `json:"schema"`
	Dataset          string               `json:"dataset"`
	Mirrors          string               `json:"mirrors"`
	Policy           string               `json:"policy"`
	AnalyzerRevision string               `json:"analyzer_revision"`
	Instances        int                  `json:"instances"`
	Scored           int                  `json:"scored"`
	Skipped          []SkippedInstance    `json:"skipped,omitempty"`
	Systems          []SystemScore        `json:"systems"`
	Rows             []BenchRow           `json:"rows,omitempty"`
	Truncations      []analyze.Truncation `json:"truncations,omitempty"`
}

// instanceIDPattern keeps work-directory names to safe filename runes.
var instanceIDPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Run scores every dataset instance. Mirror and checkout failures skip the
// instance with an explicit reason; a malformed dataset fails the run.
func Run(ctx context.Context, snapshots SnapshotSource, options RunOptions) (BenchReport, error) {
	if snapshots == nil {
		return BenchReport{}, errors.New("snapshot source is required")
	}
	instances, err := LoadInstances(options.Dataset, options.RepoFilters, options.Limit)
	if err != nil {
		return BenchReport{}, err
	}
	if options.TokenBudget <= 0 {
		options.TokenBudget = DefaultTokenBudget
	}
	if options.TopRows < 0 {
		return BenchReport{}, errors.New("--top-rows must not be negative")
	}
	report := BenchReport{
		Schema:           ReportSchema,
		Dataset:          options.Dataset,
		Mirrors:          options.Mirrors,
		Policy:           retrieval.StructuralLexicalPolicy,
		AnalyzerRevision: analyze.AnalyzerRevision,
		Instances:        len(instances),
	}
	if options.Policy != "" {
		report.Policy = options.Policy
	}
	var panMetrics, bm25Metrics []Metrics
	for _, instance := range instances {
		row, skip, err := scoreInstance(ctx, snapshots, options, instance)
		if err != nil {
			return report, fmt.Errorf("instance %s: %w", instance.ID, err)
		}
		if skip != nil {
			report.Skipped = append(report.Skipped, *skip)
			continue
		}
		report.Scored++
		panMetrics = append(panMetrics, row.Pan)
		bm25Metrics = append(bm25Metrics, row.BM25)
		report.Rows = append(report.Rows, *row)
	}
	report.Systems = []SystemScore{
		{System: SystemPan, Cases: report.Scored, Metrics: MeanMetrics(panMetrics)},
		{System: SystemBM25, Cases: report.Scored, Metrics: MeanMetrics(bm25Metrics)},
	}
	if options.TopRows > 0 && len(report.Rows) > options.TopRows {
		report.Truncations = append(report.Truncations, analyze.Truncation{
			Field: "rows", Shown: options.TopRows, Total: len(report.Rows), Reason: "row cap",
		})
		report.Rows = report.Rows[:options.TopRows]
	}
	return report, nil
}

// scoreInstance runs one instance end to end: gold extraction, mirror
// lookup, bounded archive checkout, snapshot, retrieval, baseline, and
// scoring. A nil row with a skip records why; an error fails the run.
func scoreInstance(ctx context.Context, snapshots SnapshotSource, options RunOptions, instance Instance) (*BenchRow, *SkippedInstance, error) {
	gold := GoldPaths(instance.Patch)
	if len(gold) == 0 {
		return nil, &SkippedInstance{ID: instance.ID, Reason: "patch names no files"}, nil
	}
	mirror := MirrorPath(options.Mirrors, instance.Repo)
	if mirror == "" {
		return nil, &SkippedInstance{ID: instance.ID, Reason: fmt.Sprintf("invalid repo %q", instance.Repo)}, nil
	}
	info, err := os.Stat(mirror)
	if err != nil || !info.IsDir() {
		return nil, &SkippedInstance{ID: instance.ID, Reason: "mirror not found: " + mirror}, nil
	}
	workDir := filepath.Join(options.Work, instanceIDPattern.ReplaceAllString(instance.ID, "_"))
	if err := resetWorkDir(workDir); err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(workDir) }()
	started, extractErr := extractArchive(ctx, mirror, instance.BaseCommit, workDir)
	if extractErr != nil {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("extract archive: %w", ctx.Err())
		}
		if started {
			return nil, &SkippedInstance{ID: instance.ID, Reason: "checkout failed: " + extractErr.Error()}, nil
		}
		return nil, nil, fmt.Errorf("extract archive: %w", extractErr)
	}
	snap, err := snapshots.Snapshot(ctx, workDir)
	if err != nil {
		return nil, &SkippedInstance{ID: instance.ID, Reason: "snapshot failed: " + err.Error()}, nil
	}
	request, truncated := BoundedRequest(instance.ProblemStatement)
	cases := []retrieval.EvalCase{{
		Schema:         retrieval.EvalCaseSchema,
		ID:             instance.ID,
		SnapshotID:     snapshotID(snap),
		Request:        request,
		TokenBudget:    options.TokenBudget,
		ExpectedPaths:  gold,
		Provenance:     "swe-bench",
		Classification: "nonlexical",
		Category:       retrieval.CategorySemantic,
	}}
	evalReport, err := retrieval.Evaluate(snap, cases, options.Policy)
	if err != nil {
		return nil, &SkippedInstance{ID: instance.ID, Reason: "retrieval failed: " + err.Error()}, nil
	}
	if len(evalReport.Cases) != 1 {
		return nil, nil, fmt.Errorf("expected one eval case, got %d", len(evalReport.Cases))
	}
	panRanked := evalReport.Cases[0].SelectedPaths
	bm25Ranked := BM25Rank(snap, request, maxPathList)
	row := &BenchRow{
		InstanceID: instance.ID,
		Repo:       instance.Repo,
		GoldPaths:  RankedPaths(gold, maxPathList),
		PanPaths:   RankedPaths(panRanked, maxPathList),
		BM25Paths:  bm25Ranked,
		Pan:        Score(panRanked, gold),
		BM25:       Score(bm25Ranked, gold),
	}
	if truncated {
		row.RequestTruncated = true
	}
	return row, nil, nil
}

// snapshotID returns the snapshot's stamped identity.
func snapshotID(snap analyze.Snapshot) string {
	if snap.Status.Snapshot == nil {
		return ""
	}
	return snap.Status.Snapshot.ID
}
