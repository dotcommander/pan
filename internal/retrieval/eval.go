package retrieval

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

const (
	// EvalCaseSchema identifies one JSONL retrieval evaluation case.
	EvalCaseSchema = "pan.context-eval-case/v1"
	// EvalReportSchema identifies one retrieval evaluation report.
	EvalReportSchema = "pan.context-eval-report/v1"
	// StructuralLexicalPolicy selects structural and lexical ranking.
	StructuralLexicalPolicy = "structural-lexical/v1"
	// StructuralReferenceGraphPolicy also enables reference-graph ranking.
	StructuralReferenceGraphPolicy = "structural-reference-graph/v1"

	classificationLexical    = "lexical"
	classificationNonlexical = "nonlexical"

	// Eval case categories, mirroring retrieval-benchmark practice: symbol
	// lookups behave differently from natural-language requests.
	CategorySemantic     = "semantic"
	CategoryArchitecture = "architecture"
	CategorySymbol       = "symbol"
)

// EvalCase defines one expected retrieval result for a snapshot.
type EvalCase struct {
	Schema          string   `json:"schema"`
	ID              string   `json:"case_id"`
	SnapshotID      string   `json:"snapshot_id"`
	ArtifactIDs     []string `json:"artifact_ids,omitempty"`
	Request         string   `json:"request"`
	TokenBudget     int      `json:"token_budget"`
	ExpectedPaths   []string `json:"expected_paths,omitempty"`
	ExpectedSymbols []string `json:"expected_symbols,omitempty"`
	Provenance      string   `json:"provenance"`
	Classification  string   `json:"classification"`
	// Category optionally groups cases as semantic, architecture, or symbol.
	Category string `json:"category,omitempty"`
}

// EvalResult records one case's retrieval outcome.
type EvalResult struct {
	CaseID          string               `json:"case_id"`
	Classification  string               `json:"classification"`
	Category        string               `json:"category,omitempty"`
	TokenBudget     int                  `json:"token_budget"`
	SelectedPaths   []string             `json:"selected_paths"`
	SelectedSymbols []string             `json:"selected_symbols"`
	PathInclusion   map[string]bool      `json:"path_inclusion"`
	SymbolInclusion map[string]bool      `json:"symbol_inclusion"`
	ReciprocalRank  float64              `json:"reciprocal_rank"`
	NDCGAt10        float64              `json:"ndcg_at_10"`
	Truncations     []analyze.Truncation `json:"truncations"`
	Duration        time.Duration        `json:"duration_ns"`
}

// EvalAggregate summarizes retrieval quality across cases.
type EvalAggregate struct {
	Cases               int            `json:"cases"`
	LexicalCases        int            `json:"lexical_cases"`
	NonlexicalCases     int            `json:"nonlexical_cases"`
	PathInclusionRate   float64        `json:"path_inclusion_rate"`
	SymbolInclusionRate float64        `json:"symbol_inclusion_rate"`
	MeanReciprocalRank  float64        `json:"mean_reciprocal_rank"`
	NDCGAt10            float64        `json:"ndcg_at_10"`
	CategoryCounts      map[string]int `json:"category_counts,omitempty"`
	PromotionEligible   bool           `json:"promotion_eligible"`
}

// EvalReport contains per-case and aggregate retrieval evaluation results.
type EvalReport struct {
	Schema           string              `json:"schema"`
	PolicyID         string              `json:"policy_id"`
	SnapshotID       string              `json:"snapshot_id"`
	AnalyzerRevision string              `json:"analyzer_revision"`
	ArtifactIDs      []string            `json:"artifact_ids,omitempty"`
	Cases            []EvalResult        `json:"cases"`
	Aggregate        EvalAggregate       `json:"aggregate"`
	Graph            *ranking.GraphStats `json:"graph,omitempty"`
	// Efficiency compares packet cost against a modeled grep+read baseline;
	// present only when requested.
	Efficiency *TokenEfficiency `json:"efficiency,omitempty"`
}

// LoadEvalCases parses and validates JSONL evaluation cases from path.
func LoadEvalCases(path string) ([]EvalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var out []EvalCase
	for line := 1; s.Scan(); line++ {
		var c EvalCase
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("eval case line %d: %w", line, err)
		}
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("eval case line %d: %w", line, err)
		}
		out = append(out, c)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("eval cases are empty")
	}
	return out, nil
}
func (c EvalCase) validate() error {
	switch {
	case c.Schema != EvalCaseSchema:
		return fmt.Errorf("schema must be %s", EvalCaseSchema)
	case strings.TrimSpace(c.ID) == "":
		return errors.New("case_id is required")
	case strings.TrimSpace(c.SnapshotID) == "":
		return errors.New("snapshot_id is required")
	case strings.TrimSpace(c.Request) == "":
		return errors.New("request is required")
	case c.TokenBudget <= 0:
		return errors.New("token_budget must be positive")
	case strings.TrimSpace(c.Provenance) == "":
		return errors.New("provenance is required")
	case c.Classification != classificationLexical && c.Classification != classificationNonlexical:
		return errors.New("classification must be lexical or nonlexical")
	case len(c.ExpectedPaths) == 0 && len(c.ExpectedSymbols) == 0:
		return errors.New("expected evidence is required")
	}
	switch c.Category {
	case "", CategorySemantic, CategoryArchitecture, CategorySymbol:
	default:
		return fmt.Errorf("category must be semantic, architecture, or symbol")
	}
	return nil
}

// EvalOptions shapes optional evaluation passes.
type EvalOptions struct {
	// Efficiency adds the grep+read baseline comparison and recall-at-budget
	// curves to the report.
	Efficiency bool
}

// Evaluate scores retrieval results against the supplied cases under policy.
func Evaluate(snap analyze.Snapshot, cases []EvalCase, policy string, options ...EvalOptions) (EvalReport, error) {
	opts := EvalOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	if policy == "" {
		policy = StructuralLexicalPolicy
	}
	graphPolicy := policy == StructuralReferenceGraphPolicy
	if policy != StructuralLexicalPolicy && !graphPolicy {
		return EvalReport{}, fmt.Errorf("unsupported retrieval policy %q", policy)
	}
	if snap.Status.Snapshot == nil || snap.Status.Snapshot.ID == "" {
		return EvalReport{}, errors.New("snapshot identity is required")
	}
	r := EvalReport{Schema: EvalReportSchema, PolicyID: policy, SnapshotID: snap.Status.Snapshot.ID, AnalyzerRevision: analyze.AnalyzerRevision, Cases: []EvalResult{}}
	counts := evalInclusionCounts{}
	packets := make([]TaskReport, 0, len(cases))
	for _, c := range cases {
		if err := c.validate(); err != nil {
			return EvalReport{}, fmt.Errorf("case %s: %w", c.ID, err)
		}
		if c.SnapshotID != r.SnapshotID {
			return EvalReport{}, fmt.Errorf("snapshot mismatch for %s", c.ID)
		}
		result, packet, stats, err := evaluateCase(snap, c, policy, graphPolicy)
		if err != nil {
			return EvalReport{}, err
		}
		packets = append(packets, packet)
		counts = appendEvalResult(&r, evalResultUpdate{caseInput: c, result: result, stats: stats, graphPolicy: graphPolicy}, counts)
	}
	slices.Sort(r.ArtifactIDs)
	r.ArtifactIDs = slices.Compact(r.ArtifactIDs)
	if counts.pathExpected > 0 {
		r.Aggregate.PathInclusionRate = float64(counts.pathFound) / float64(counts.pathExpected)
	}
	if counts.symbolExpected > 0 {
		r.Aggregate.SymbolInclusionRate = float64(counts.symbolFound) / float64(counts.symbolExpected)
	}
	if r.Aggregate.Cases > 0 {
		r.Aggregate.MeanReciprocalRank /= float64(r.Aggregate.Cases)
		r.Aggregate.NDCGAt10 /= float64(r.Aggregate.Cases)
	}
	r.Aggregate.PromotionEligible = r.Aggregate.LexicalCases >= 25 && r.Aggregate.NonlexicalCases >= 25
	if opts.Efficiency {
		efficiency, err := evaluateEfficiency(snap, cases, packets)
		if err != nil {
			return EvalReport{}, err
		}
		r.Efficiency = efficiency
	}
	return r, nil
}

type evalInclusionCounts struct {
	pathExpected, pathFound     int
	symbolExpected, symbolFound int
}

type evalResultUpdate struct {
	caseInput   EvalCase
	result      EvalResult
	stats       ranking.GraphStats
	graphPolicy bool
}

func appendEvalResult(report *EvalReport, update evalResultUpdate, counts evalInclusionCounts) evalInclusionCounts {
	report.ArtifactIDs = append(report.ArtifactIDs, update.caseInput.ArtifactIDs...)
	updateEvalGraph(report, update.stats, update.graphPolicy)
	report.Cases = append(report.Cases, update.result)
	report.Aggregate.Cases++
	if update.caseInput.Classification == classificationLexical {
		report.Aggregate.LexicalCases++
	} else {
		report.Aggregate.NonlexicalCases++
	}
	if update.caseInput.Category != "" {
		if report.Aggregate.CategoryCounts == nil {
			report.Aggregate.CategoryCounts = make(map[string]int)
		}
		report.Aggregate.CategoryCounts[update.caseInput.Category]++
	}
	counts.pathExpected, counts.pathFound = inclusionCounts(update.result.PathInclusion, counts.pathExpected, counts.pathFound)
	counts.symbolExpected, counts.symbolFound = inclusionCounts(update.result.SymbolInclusion, counts.symbolExpected, counts.symbolFound)
	report.Aggregate.MeanReciprocalRank += update.result.ReciprocalRank
	report.Aggregate.NDCGAt10 += update.result.NDCGAt10
	return counts
}

func inclusionCounts(values map[string]bool, expected, found int) (updatedExpected, updatedFound int) {
	for _, included := range values {
		expected++
		if included {
			found++
		}
	}
	return expected, found
}

func evaluateCase(snap analyze.Snapshot, c EvalCase, policy string, graphPolicy bool) (EvalResult, TaskReport, ranking.GraphStats, error) {
	start := time.Now()
	ranked, stats := ranking.RankWithStats(snap, "", ranking.Options{Intent: c.Request, ReferenceGraph: graphPolicy})
	packet, err := Task(snap, ranked, c.Request, TaskOptions{Tokens: c.TokenBudget, PolicyID: policy})
	if err != nil {
		return EvalResult{}, TaskReport{}, ranking.GraphStats{}, fmt.Errorf("case %s: %w", c.ID, err)
	}
	result := scoreEvalCase(c, packet)
	result.Duration = time.Since(start)
	return result, packet, stats, nil
}

func updateEvalGraph(report *EvalReport, stats ranking.GraphStats, graphPolicy bool) {
	if !graphPolicy {
		return
	}
	if report.Graph == nil {
		report.Graph = &ranking.GraphStats{}
	}
	report.Graph.Nodes = max(report.Graph.Nodes, stats.Nodes)
	report.Graph.Edges = max(report.Graph.Edges, stats.Edges)
	report.Graph.Iterations = max(report.Graph.Iterations, stats.Iterations)
}

func scoreEvalCase(c EvalCase, packet TaskReport) EvalResult {
	r := EvalResult{CaseID: c.ID, Classification: c.Classification, Category: c.Category, TokenBudget: c.TokenBudget, PathInclusion: map[string]bool{}, SymbolInclusion: map[string]bool{}, Truncations: slices.Clone(packet.Truncations)}
	for _, target := range packet.Targets {
		r.SelectedPaths = append(r.SelectedPaths, target.Path)
		for _, symbol := range target.Symbols {
			r.SelectedSymbols = append(r.SelectedSymbols, symbol.Name)
		}
	}
	first := 0
	for _, want := range c.ExpectedPaths {
		idx := slices.Index(r.SelectedPaths, want)
		r.PathInclusion[want] = idx >= 0
		if idx >= 0 && (first == 0 || idx+1 < first) {
			first = idx + 1
		}
	}
	for _, want := range c.ExpectedSymbols {
		idx := slices.Index(r.SelectedSymbols, want)
		r.SymbolInclusion[want] = idx >= 0
		if idx >= 0 && (first == 0 || idx+1 < first) {
			first = idx + 1
		}
	}
	if first > 0 {
		r.ReciprocalRank = 1 / float64(first)
	}
	r.NDCGAt10 = caseNDCG(c, packet)
	return r
}

// caseNDCG scores packet target ordering with binary relevance and a
// logarithmic position discount over the first ten targets, so orderings
// MRR cannot distinguish (multiple expected targets) still move the score.
// A target is relevant when it covers an expected path or expected symbol.
func caseNDCG(c EvalCase, packet TaskReport) float64 {
	relevant := make(map[int]bool)
	for idx, target := range packet.Targets {
		for _, want := range c.ExpectedPaths {
			if target.Path == want {
				relevant[idx] = true
			}
		}
		for _, want := range c.ExpectedSymbols {
			for _, symbol := range target.Symbols {
				if symbol.Name == want {
					relevant[idx] = true
				}
			}
		}
	}
	if len(relevant) == 0 {
		return 0
	}
	dcg := 0.0
	for idx := range packet.Targets {
		if relevant[idx] && idx < 10 {
			dcg += 1 / math.Log2(float64(idx+2))
		}
	}
	idcg := 0.0
	for i := range min(len(relevant), 10) {
		idcg += 1 / math.Log2(float64(i+2))
	}
	return dcg / idcg
}
