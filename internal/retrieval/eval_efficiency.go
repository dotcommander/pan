package retrieval

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// Token-efficiency evaluation models the workflow an agent falls back to
// without retrieval tooling: every goal term greps captured sources as a
// fixed string, matched files are read in full ranked by distinct term
// count, and a case is covered at the first read containing an expected
// path. Costs use the established four-bytes-per-token estimate.

const (
	// efficiencySchema identifies the token-efficiency summary shape.
	efficiencySchema = "pan.context-eval-efficiency/v1"
	// efficiencyMissTokens caps the cost of a case whose relevant path is
	// never covered by a method.
	efficiencyMissTokens = 32_000
	// efficiencyMethodBaseline models grep+read; efficiencyMethodPacket is
	// the bounded task packet itself.
	efficiencyMethodBaseline = "grep+read (modeled)"
	efficiencyMethodPacket   = "task packet"
)

// efficiencyBudgetPoints are the token budgets reported by the recall
// curves, matching common context-window scales.
var efficiencyBudgetPoints = []int{500, 1000, 2000, 4000, 8000, 16000, 32000}

// BudgetRecall is one recall observation at a fixed token budget.
type BudgetRecall struct {
	Tokens int     `json:"tokens"`
	Recall float64 `json:"recall"`
}

// MethodEfficiency summarizes one retrieval method across all cases.
type MethodEfficiency struct {
	Name            string         `json:"name"`
	Cases           int            `json:"cases"`
	ExpectedTokens  float64        `json:"expected_tokens_to_first_relevant"`
	RecallAtBudget  []BudgetRecall `json:"recall_at_budget"`
	CoveredCases    int            `json:"covered_cases"`
	SkippedNoSource int            `json:"skipped_no_source,omitempty"`
}

// TokenEfficiency compares the task packet against the modeled grep+read
// baseline. Savings tokens are reported for each method at the same budgets.
type TokenEfficiency struct {
	Schema  string             `json:"schema"`
	Methods []MethodEfficiency `json:"methods"`
}

// evaluateEfficiency computes per-case coverage costs for the packet and the
// modeled baseline, then aggregates them into recall curves and expected
// token counts. Files without captured source are skipped by the baseline
// and counted, so partial snapshots never overstate baseline cost.
func evaluateEfficiency(snap analyze.Snapshot, cases []EvalCase, packets []TaskReport) (*TokenEfficiency, error) {
	if len(cases) != len(packets) {
		return nil, fmt.Errorf("efficiency needs one packet per case: %d cases, %d packets", len(cases), len(packets))
	}
	packetMethod := MethodEfficiency{Name: efficiencyMethodPacket, Cases: len(cases)}
	baselineMethod := MethodEfficiency{Name: efficiencyMethodBaseline, Cases: len(cases)}
	packetCosts := make([]float64, len(cases))
	baselineCosts := make([]float64, len(cases))
	for i, c := range cases {
		packetCosts[i] = packetFirstRelevantTokens(cases[i], packets[i])
		if packetCosts[i] < efficiencyMissTokens {
			packetMethod.CoveredCases++
		}
		cost, skipped := baselineFirstRelevantTokens(snap, c)
		baselineCosts[i] = cost
		baselineMethod.SkippedNoSource += skipped
		if cost < efficiencyMissTokens {
			baselineMethod.CoveredCases++
		}
	}
	packetMethod.ExpectedTokens = mean(packetCosts)
	baselineMethod.ExpectedTokens = mean(baselineCosts)
	packetMethod.RecallAtBudget = recallAtBudget(packetCosts)
	baselineMethod.RecallAtBudget = recallAtBudget(baselineCosts)
	return &TokenEfficiency{Schema: efficiencySchema, Methods: []MethodEfficiency{packetMethod, baselineMethod}}, nil
}

// packetFirstRelevantTokens reports the cumulative encoded packet tokens up
// to and including the first target covering an expected path or symbol, or
// the miss cap when no target covers the case.
func packetFirstRelevantTokens(c EvalCase, packet TaskReport) float64 {
	cumulative := 0
	for _, target := range packet.Targets {
		encoded, err := json.Marshal(target)
		if err != nil {
			continue
		}
		cumulative += (len(encoded) + 3) / 4
		if targetCoversCase(target, c) {
			return float64(cumulative)
		}
	}
	return efficiencyMissTokens
}

func targetCoversCase(target TaskTarget, c EvalCase) bool {
	for _, want := range c.ExpectedPaths {
		if target.Path == want {
			return true
		}
	}
	for _, want := range c.ExpectedSymbols {
		for _, symbol := range target.Symbols {
			if symbol.Name == want {
				return true
			}
		}
	}
	return false
}

// baselineFirstRelevantTokens models grep+read: goal terms (three runes or
// longer, stopwords dropped, sub-token expanded) are searched as fixed
// strings over every captured source; matched files are read in full ranked
// by distinct matched-term count then path; the cost is cumulative read
// tokens through the first file covering the case.
func baselineFirstRelevantTokens(snap analyze.Snapshot, c EvalCase) (cost float64, skippedNoSource int) {
	terms := baselineTerms(c.Request)
	type match struct {
		path       string
		distinct   int
		readTokens int
		relevant   bool
	}
	var matches []match
	for _, file := range snap.Files {
		source, ok := snap.Source(file.Path)
		if !ok {
			skippedNoSource++
			continue
		}
		lower := strings.ToLower(string(source))
		distinct := 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				distinct++
			}
		}
		if distinct == 0 {
			continue
		}
		matches = append(matches, match{
			path:       file.Path,
			distinct:   distinct,
			readTokens: (len(source) + 3) / 4,
			relevant:   slices.Contains(c.ExpectedPaths, file.Path),
		})
	}
	slices.SortStableFunc(matches, func(a, b match) int {
		if a.distinct != b.distinct {
			return b.distinct - a.distinct
		}
		return strings.Compare(a.path, b.path)
	})
	cumulative := 0
	for _, m := range matches {
		cumulative += m.readTokens
		if m.relevant {
			return float64(cumulative), skippedNoSource
		}
	}
	return efficiencyMissTokens, skippedNoSource
}

// baselineTerms extracts grep keywords with the shared expansion rules at
// the ranking length floor: three runes minimum, stopwords dropped, compound
// identifiers expanded so ParseConfig also greps as its sub-tokens.
func baselineTerms(request string) []string {
	return ranking.ExpandTerms(request, 3, ranking.CommonStopwords)
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

// recallAtBudget reports the fraction of cases covered within each budget
// point, spending each case's cost once.
func recallAtBudget(costs []float64) []BudgetRecall {
	out := make([]BudgetRecall, 0, len(efficiencyBudgetPoints))
	for _, budget := range efficiencyBudgetPoints {
		covered := 0
		for _, cost := range costs {
			if cost <= float64(budget) {
				covered++
			}
		}
		out = append(out, BudgetRecall{Tokens: budget, Recall: float64(covered) / float64(len(costs))})
	}
	return out
}
