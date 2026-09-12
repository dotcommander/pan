package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/fileclass"
)

// CustomPatterns is a portable, deterministic local scoring catalog.
// Its contents are never sent to a provider.
type CustomPatterns struct {
	PathTerms []PathTerm       `json:"path_terms"`
	Content   []ContentPattern `json:"content_patterns"`
}

// PathTerm raises the risk score for paths containing Term.
type PathTerm struct {
	Term   string `json:"term"`
	Weight int    `json:"weight"`
}

// ContentPattern raises the risk score for up to MaxMatches regular-expression
// matches in a source file.
type ContentPattern struct {
	ID         string `json:"id"`
	Pattern    string `json:"pattern"`
	Weight     int    `json:"weight"`
	MaxMatches int    `json:"max_matches"`
}

type compiledPattern struct {
	ContentPattern
	re *regexp2.Regexp
}

// LoadCustomPatterns validates a Pan-compatible patterns JSON document.
func LoadCustomPatterns(data []byte) (CustomPatterns, error) {
	var patterns CustomPatterns
	if err := json.Unmarshal(data, &patterns); err != nil {
		return CustomPatterns{}, fmt.Errorf("parse patterns: %w", err)
	}
	if len(patterns.PathTerms) == 0 && len(patterns.Content) == 0 {
		return CustomPatterns{}, errors.New("patterns must define path_terms or content_patterns")
	}
	for i, term := range patterns.PathTerms {
		if strings.TrimSpace(term.Term) == "" || term.Weight <= 0 {
			return CustomPatterns{}, fmt.Errorf("invalid path_terms[%d]", i)
		}
	}
	for i, item := range patterns.Content {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Pattern) == "" || item.Weight <= 0 || item.MaxMatches <= 0 {
			return CustomPatterns{}, fmt.Errorf("invalid content_patterns[%d]", i)
		}
	}
	return patterns, nil
}

// LoadCustomPatternsFile reads and validates a local pattern catalog.
func LoadCustomPatternsFile(name string) (CustomPatterns, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return CustomPatterns{}, fmt.Errorf("read patterns %s: %w", name, err)
	}
	return LoadCustomPatterns(data)
}

// RiskWithCustomPatterns adds an optional local catalog to Pan's built-in risk
// signals. A nil catalog has precisely Risk's default behavior.
func RiskWithCustomPatterns(ctx context.Context, snap analyze.Snapshot, top int, patterns *CustomPatterns) (RiskReport, error) {
	if patterns == nil {
		return Risk(ctx, snap, top)
	}
	report, err := Risk(ctx, snap, 0)
	if err != nil {
		return RiskReport{}, err
	}
	compiled, err := compileCustomPatterns(*patterns)
	if err != nil {
		return RiskReport{}, err
	}
	if err := applyCustomPatterns(ctx, snap, patterns.PathTerms, compiled, &report); err != nil {
		return RiskReport{}, err
	}
	return finalizeCustomRisk(report, top), nil
}

func compileCustomPatterns(patterns CustomPatterns) ([]compiledPattern, error) {
	compiled := make([]compiledPattern, 0, len(patterns.Content))
	for _, item := range patterns.Content {
		re, err := regexp2.Compile(item.Pattern, regexp2.None)
		if err != nil {
			return nil, fmt.Errorf("compile pattern %s: %w", item.ID, err)
		}
		re.MatchTimeout = 100 * time.Millisecond
		compiled = append(compiled, compiledPattern{ContentPattern: item, re: re})
	}
	return compiled, nil
}

func applyCustomPatterns(ctx context.Context, snap analyze.Snapshot, terms []PathTerm, patterns []compiledPattern, report *RiskReport) error {
	byPath := make(map[string]int, len(report.Files))
	for i := range report.Files {
		byPath[report.Files[i].Path] = i
	}
	for _, file := range snap.Files {
		if fileclass.Classify(file.Path, file.Language, file.Generated) != fileclass.Production {
			continue
		}
		index, found := byPath[file.Path]
		if !found {
			report.Files = append(report.Files, FileRisk{Path: file.Path, FileClass: string(fileclass.Production), Confidence: evidenceHeuristic})
			index = len(report.Files) - 1
			byPath[file.Path] = index
		}
		if err := addCustomRisk(ctx, snap.Root, file.Path, customRiskRules{terms, patterns}, &report.Files[index]); err != nil {
			return err
		}
	}
	return nil
}

func finalizeCustomRisk(report RiskReport, top int) RiskReport {
	for i := range report.Files {
		report.Files[i].ReviewPriority = report.Files[i].Score
	}
	report.Files = slices.DeleteFunc(report.Files, func(risk FileRisk) bool { return risk.Score == 0 })
	slices.SortFunc(report.Files, compareCustomRisk)
	lanes := map[string][]string{}
	for _, risk := range report.Files {
		for _, lane := range risk.Lanes {
			lanes[lane] = append(lanes[lane], risk.Path)
		}
	}
	report.Lanes = buildRiskLanes(lanes)
	total := len(report.Files)
	if top > 0 && total > top {
		report.Files = report.Files[:top]
		report.FilesOmittedReason = fmt.Sprintf("showing %d of %d scored files; truncated by --top", len(report.Files), total)
	}
	report.Analysis.ScoredFiles = total
	report.Analysis.ReturnedFiles = len(report.Files)
	return report
}

func compareCustomRisk(left, right FileRisk) int {
	if left.Score != right.Score {
		return right.Score - left.Score
	}
	return strings.Compare(left.Path, right.Path)
}

type customRiskRules struct {
	terms    []PathTerm
	patterns []compiledPattern
}

func addCustomRisk(ctx context.Context, root, rel string, rules customRiskRules, risk *FileRisk) error {
	// Preserve the historical custom-catalog reason ordering for callers that
	// display the built-in marker once per source occurrence; structured
	// score_components remain deduplicated by the built-in scorer.
	if len(rules.patterns) > 0 && slices.Contains(risk.Reasons, "change marker") {
		risk.Reasons = append(risk.Reasons, "change marker")
	}
	for _, term := range rules.terms {
		if pathTermMatches(strings.ToLower(rel), strings.ToLower(term.Term)) {
			risk.Score += term.Weight
			risk.Lanes = appendUnique(risk.Lanes, laneBestPractices)
			risk.Reasons = append(risk.Reasons, "custom:path:"+term.Term)
			risk.ScoreComponents = append(risk.ScoreComponents, RiskScoreComponent{ID: "custom:path:" + term.Term, Lane: laneBestPractices, EvidenceKind: "configured-path", Confidence: evidenceHeuristic, Points: term.Weight, Matches: 1, TotalMatches: 1, CountedMatches: 1, Reason: "custom path term " + term.Term})
		}
	}
	if len(rules.patterns) == 0 {
		risk.Reasons = capReasons(risk.Reasons)
		return nil
	}
	lines, _, err := readLines(ctx, path.Join(root, filepathFromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read custom pattern source %s: %w", rel, err)
	}
	text := make([]string, len(lines))
	for i, line := range lines {
		text[i] = line.text
	}
	for _, pattern := range rules.patterns {
		count, err := customMatchCount(pattern.re, strings.Join(text, "\n"), pattern.MaxMatches)
		if err != nil {
			return fmt.Errorf("match custom pattern %s: %w", pattern.ID, err)
		}
		if count > 0 {
			risk.Score += count * pattern.Weight
			risk.Lanes = appendUnique(risk.Lanes, laneBestPractices)
			risk.Reasons = append(risk.Reasons, fmt.Sprintf("custom:content:%s:%d", pattern.ID, count))
			risk.ScoreComponents = append(risk.ScoreComponents, RiskScoreComponent{ID: "custom:content:" + pattern.ID, Lane: laneBestPractices, EvidenceKind: "configured-source-pattern", Confidence: evidenceHeuristic, Points: count * pattern.Weight, Matches: count, TotalMatches: count, CountedMatches: count, Capped: count == pattern.MaxMatches, Reason: "custom content pattern " + pattern.ID})
		}
	}
	risk.Reasons = capReasons(risk.Reasons)
	return nil
}

func customMatchCount(re *regexp2.Regexp, text string, max int) (int, error) {
	count := 0
	match, err := re.FindStringMatch(text)
	for match != nil && err == nil && count < max {
		count++
		match, err = re.FindNextMatch(match)
	}
	return count, err
}
