package retrieval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

const (
	EvalCaseSchema                 = "pan.context-eval-case/v1"
	EvalReportSchema               = "pan.context-eval-report/v1"
	StructuralLexicalPolicy        = "structural-lexical/v1"
	StructuralReferenceGraphPolicy = "structural-reference-graph/v1"
)

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
}
type EvalResult struct {
	CaseID          string               `json:"case_id"`
	Classification  string               `json:"classification"`
	TokenBudget     int                  `json:"token_budget"`
	SelectedPaths   []string             `json:"selected_paths"`
	SelectedSymbols []string             `json:"selected_symbols"`
	PathInclusion   map[string]bool      `json:"path_inclusion"`
	SymbolInclusion map[string]bool      `json:"symbol_inclusion"`
	ReciprocalRank  float64              `json:"reciprocal_rank"`
	Truncations     []analyze.Truncation `json:"truncations"`
	Duration        time.Duration        `json:"duration_ns"`
}
type EvalAggregate struct {
	Cases               int     `json:"cases"`
	LexicalCases        int     `json:"lexical_cases"`
	NonlexicalCases     int     `json:"nonlexical_cases"`
	PathInclusionRate   float64 `json:"path_inclusion_rate"`
	SymbolInclusionRate float64 `json:"symbol_inclusion_rate"`
	MeanReciprocalRank  float64 `json:"mean_reciprocal_rank"`
	PromotionEligible   bool    `json:"promotion_eligible"`
}
type EvalReport struct {
	Schema           string              `json:"schema"`
	PolicyID         string              `json:"policy_id"`
	SnapshotID       string              `json:"snapshot_id"`
	AnalyzerRevision string              `json:"analyzer_revision"`
	ArtifactIDs      []string            `json:"artifact_ids,omitempty"`
	Cases            []EvalResult        `json:"cases"`
	Aggregate        EvalAggregate       `json:"aggregate"`
	Graph            *ranking.GraphStats `json:"graph,omitempty"`
}

func LoadEvalCases(path string) ([]EvalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
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
		return nil, fmt.Errorf("eval cases are empty")
	}
	return out, nil
}
func (c EvalCase) validate() error {
	switch {
	case c.Schema != EvalCaseSchema:
		return fmt.Errorf("schema must be %s", EvalCaseSchema)
	case strings.TrimSpace(c.ID) == "":
		return fmt.Errorf("case_id is required")
	case strings.TrimSpace(c.SnapshotID) == "":
		return fmt.Errorf("snapshot_id is required")
	case strings.TrimSpace(c.Request) == "":
		return fmt.Errorf("request is required")
	case c.TokenBudget <= 0:
		return fmt.Errorf("token_budget must be positive")
	case strings.TrimSpace(c.Provenance) == "":
		return fmt.Errorf("provenance is required")
	case c.Classification != "lexical" && c.Classification != "nonlexical":
		return fmt.Errorf("classification must be lexical or nonlexical")
	case len(c.ExpectedPaths) == 0 && len(c.ExpectedSymbols) == 0:
		return fmt.Errorf("expected evidence is required")
	}
	return nil
}
func Evaluate(snap analyze.Snapshot, cases []EvalCase, policy string) (EvalReport, error) {
	if policy == "" {
		policy = StructuralLexicalPolicy
	}
	graphPolicy := policy == StructuralReferenceGraphPolicy
	if policy != StructuralLexicalPolicy && !graphPolicy {
		return EvalReport{}, fmt.Errorf("unsupported retrieval policy %q", policy)
	}
	if snap.Status.Snapshot == nil || snap.Status.Snapshot.ID == "" {
		return EvalReport{}, fmt.Errorf("snapshot identity is required")
	}
	r := EvalReport{Schema: EvalReportSchema, PolicyID: policy, SnapshotID: snap.Status.Snapshot.ID, AnalyzerRevision: analyze.AnalyzerRevision, Cases: []EvalResult{}}
	var pe, pf, se, sf int
	for _, c := range cases {
		if err := c.validate(); err != nil {
			return EvalReport{}, fmt.Errorf("case %s: %w", c.ID, err)
		}
		if c.SnapshotID != r.SnapshotID {
			return EvalReport{}, fmt.Errorf("snapshot mismatch for %s", c.ID)
		}
		r.ArtifactIDs = append(r.ArtifactIDs, c.ArtifactIDs...)
		start := time.Now()
		ranked, stats := ranking.RankWithStats(snap, "", ranking.Options{Intent: c.Request, ReferenceGraph: graphPolicy})
		if graphPolicy {
			if r.Graph == nil {
				r.Graph = &ranking.GraphStats{}
			}
			r.Graph.Nodes = max(r.Graph.Nodes, stats.Nodes)
			r.Graph.Edges = max(r.Graph.Edges, stats.Edges)
			r.Graph.Iterations = max(r.Graph.Iterations, stats.Iterations)
		}
		packet, err := Task(snap, ranked, c.Request, TaskOptions{Tokens: c.TokenBudget, PolicyID: policy})
		if err != nil {
			return EvalReport{}, fmt.Errorf("case %s: %w", c.ID, err)
		}
		result := scoreEvalCase(c, packet)
		result.Duration = time.Since(start)
		r.Cases = append(r.Cases, result)
		r.Aggregate.Cases++
		if c.Classification == "lexical" {
			r.Aggregate.LexicalCases++
		} else {
			r.Aggregate.NonlexicalCases++
		}
		for _, v := range result.PathInclusion {
			pe++
			if v {
				pf++
			}
		}
		for _, v := range result.SymbolInclusion {
			se++
			if v {
				sf++
			}
		}
		r.Aggregate.MeanReciprocalRank += result.ReciprocalRank
	}
	slices.Sort(r.ArtifactIDs)
	r.ArtifactIDs = slices.Compact(r.ArtifactIDs)
	if pe > 0 {
		r.Aggregate.PathInclusionRate = float64(pf) / float64(pe)
	}
	if se > 0 {
		r.Aggregate.SymbolInclusionRate = float64(sf) / float64(se)
	}
	if r.Aggregate.Cases > 0 {
		r.Aggregate.MeanReciprocalRank /= float64(r.Aggregate.Cases)
	}
	r.Aggregate.PromotionEligible = r.Aggregate.LexicalCases >= 25 && r.Aggregate.NonlexicalCases >= 25
	return r, nil
}
func scoreEvalCase(c EvalCase, packet TaskReport) EvalResult {
	r := EvalResult{CaseID: c.ID, Classification: c.Classification, TokenBudget: c.TokenBudget, PathInclusion: map[string]bool{}, SymbolInclusion: map[string]bool{}, Truncations: slices.Clone(packet.Truncations)}
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
	return r
}
