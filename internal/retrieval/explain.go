package retrieval

import (
	"fmt"
	"path"
	"slices"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// Parse methods reported by Explain; they name the analyzer that produced a
// file's symbols, so consumers can weigh the evidence accordingly.
const (
	ParseGoAST     = "go_ast"
	ParseInventory = "inventory"
)

// ExplainResult describes why one file ranked and rendered the way it did.
type ExplainResult struct {
	File            FileSummary       `json:"file"`
	Score           int               `json:"score"`
	ScoreComponents map[string]int    `json:"score_components,omitempty"`
	ComponentTotal  int               `json:"component_total"`
	DetailLevel     int               `json:"detail_level"`
	SymbolCount     int               `json:"symbol_count"`
	Symbols         []analyze.Symbol  `json:"symbols,omitempty"`
	OmittedReason   string            `json:"omitted_reason,omitempty"`
	ScoreByTier     map[string]int    `json:"score_by_tier,omitempty"`
	ComponentTiers  map[string]string `json:"component_tiers,omitempty"`
	ParseMethod     string            `json:"parse_method"`
	ParseConfidence string            `json:"parse_confidence,omitempty"`
}

// Explain reports score and budget evidence for relPath. tokens applies the
// enriched-mode budget assignment so DetailLevel and OmittedReason reflect
// what `context map --tokens N` would render; tokens <= 0 means unlimited.
func Explain(ranked []ranking.RankedFile, relPath string, tokens int) (ExplainResult, error) {
	relPath = path.Clean(relPath)
	target, ok := rankedByPath(ranked, relPath)
	if !ok {
		return ExplainResult{}, fmt.Errorf("file %q not found in analyzed snapshot", relPath)
	}
	// Explain is a targeted query. Budget only the requested file instead of
	// letting unrelated ranked-file headers consume its symbol budget.
	targets := []ranking.RankedFile{target}
	ranking.AssignBudget(targets, tokens, explainSymbolCost)
	target = targets[0]
	result := ExplainResult{
		File:           fileSummary(target),
		Score:          target.Score,
		ComponentTotal: scoreComponentTotal(target),
		DetailLevel:    target.DetailLevel,
		SymbolCount:    len(target.Symbols),
		ParseMethod:    parseMethod(target),
	}
	if target.DetailLevel == 2 {
		result.Symbols = slices.Clone(target.Symbols)
	}
	switch {
	case len(target.Symbols) == 0:
		result.OmittedReason = "no symbol evidence was available for this file"
	case target.DetailLevel < 0:
		result.OmittedReason = "omitted: token budget could not fit the file header"
	case target.DetailLevel == 0:
		result.OmittedReason = "header only: token budget did not fit symbol detail"
	case target.DetailLevel == 1:
		result.OmittedReason = "summary only: token budget did not fit full symbols"
	}
	if len(target.Components) > 0 {
		result.ScoreComponents = target.Components
		result.ScoreByTier = make(map[string]int, 3)
		result.ComponentTiers = make(map[string]string, len(target.Components))
		for component, value := range target.Components {
			tier := ranking.TierOf(component)
			result.ComponentTiers[component] = tier
			result.ScoreByTier[tier] += value
		}
	}
	if result.ParseMethod == ParseGoAST {
		result.ParseConfidence = ranking.TierConfirmed
	}
	return result, nil
}

// explainSymbolCost conservatively estimates JSON symbol evidence for both
// exported and unexported declarations. A targeted explanation must not hide
// package-local owners merely because the repository map omits them.
func explainSymbolCost(symbols []analyze.Symbol) int {
	cost := 0
	for _, symbol := range symbols {
		cost += 32 + len(symbol.Name) + len(symbol.Kind) + len(symbol.Signature) + len(symbol.Receiver) + len(symbol.Doc)
	}
	return cost
}

func scoreComponentTotal(rf ranking.RankedFile) int {
	total := 0
	for _, value := range rf.Components {
		total += value
	}
	return total
}

// parseMethod names the analyzer behind a file's symbols. Only Go files are
// parsed; everything else contributes inventory facts only.
func parseMethod(rf ranking.RankedFile) string {
	if rf.Language == languageGo {
		return ParseGoAST
	}
	return ParseInventory
}
