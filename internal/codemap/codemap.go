// Package codemap renders ranked files into a deterministic, token-budgeted
// repository map. Cost estimators in this package are the single source of
// truth for both budget assignment and rendering, so the two cannot drift;
// every bound applied to the output is reported through truncation records.
package codemap

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// Rendering modes. Enriched shows exported signatures with doc sentences;
// compact shows exported names only and therefore fits more files per token.
const (
	ModeEnriched    = "enriched"
	ModeCompact     = "compact"
	ModeVerbose     = "verbose"
	ModeDetail      = "detail"
	ModeLines       = "lines"
	ModeXML         = "xml"
	kindFunction    = "function"
	languageUnknown = "unknown"
	languageGo      = "go"
)

// Options shapes one map build.
type Options struct {
	// Mode selects the renderer; empty means ModeEnriched.
	Mode string
	// Tokens is the approximate output budget; <= 0 means unlimited.
	Tokens int
	// Intent orients file ranking toward a task (see ranking.Options).
	Intent string
	// Consumed are repo-relative paths already in the agent's context.
	Consumed          []string
	Root              string
	Calls             bool
	CallsThreshold    int
	CallsLimit        int
	CallsIncludeTests bool
	SymbolRefs        bool
	ExplainScores     bool
	IncludeTests      bool
	Edges             []analyze.Edge
}

// Result is the rendered map plus its accounting. Text is the map itself;
// every count and truncation describes exactly what Text contains.
type Result struct {
	Mode         string               `json:"mode"`
	TokenBudget  int                  `json:"token_budget"`
	UsedTokens   int                  `json:"used_tokens"`
	TotalFiles   int                  `json:"total_files"`
	TotalSymbols int                  `json:"total_symbols"`
	ShownFiles   int                  `json:"shown_files"`
	ShownSymbols int                  `json:"shown_symbols"`
	OmittedFiles int                  `json:"omitted_files"`
	DetailLevels map[string]int       `json:"detail_levels"`
	Truncations  []analyze.Truncation `json:"truncations,omitempty"`
	Text         string               `json:"text"`
	CallEvidence []CallEvidence       `json:"call_evidence,omitempty"`
}

// Build renders ranked into a budgeted map. ranked must come from
// ranking.Rank over the same snapshot; Build assigns detail levels in place,
// so callers treating ranked as immutable should pass a copy.
func Build(ranked []ranking.RankedFile, opts Options) Result {
	mode := opts.Mode
	if mode == "" {
		mode = ModeEnriched
	}
	ranked = slices.DeleteFunc(ranked, func(file ranking.RankedFile) bool { return file.Language == languageUnknown })
	cost := EnrichedCost
	if mode == ModeCompact {
		cost = CompactCost
	}
	if mode == ModeVerbose || mode == ModeDetail {
		for i := range ranked {
			ranked[i].DetailLevel = 2
		}
	} else {
		ranking.AssignBudget(ranked, opts.Tokens, cost)
	}
	calls := collectCallEvidence(ranked, opts)
	var body string
	switch mode {
	case ModeVerbose:
		body = renderVerbose(ranked, false, opts.ExplainScores, calls)
	case ModeDetail:
		body = renderVerbose(ranked, true, opts.ExplainScores, calls)
	case ModeLines:
		body = renderLines(ranked, opts.Root, opts.Tokens)
	case ModeXML:
		body = renderXML(ranked)
	default:
		body = renderBodyWithOptions(ranked, mode, opts.ExplainScores, calls)
	}
	omitted := 0
	shownSymbols := 0
	levels := make(map[string]int, 3)
	for _, f := range ranked {
		if f.DetailLevel < 0 {
			omitted++
		} else {
			levels[strconv.Itoa(f.DetailLevel)]++
			shownSymbols += len(f.Symbols)
		}
	}
	text := body
	if mode != ModeXML {
		text = renderHeader(mode, len(ranked), countSymbols(ranked), estimateTokens(body), opts.Tokens) + body
	}
	result := Result{
		Mode:         mode,
		TokenBudget:  opts.Tokens,
		UsedTokens:   estimateTokens(text),
		TotalFiles:   len(ranked),
		TotalSymbols: countSymbols(ranked),
		ShownFiles:   len(ranked) - omitted,
		ShownSymbols: shownSymbols,
		OmittedFiles: omitted,
		DetailLevels: levels,
		Text:         text,
		CallEvidence: calls,
	}
	if omitted > 0 {
		result.Truncations = append(result.Truncations, analyze.Truncation{
			Field:  "files",
			Shown:  result.ShownFiles,
			Total:  result.TotalFiles,
			Reason: "token budget",
		})
	}
	return result
}

// countSymbols totals symbols across every file, including omitted ones, so
// the header reports repository truth rather than rendered truth.
func countSymbols(ranked []ranking.RankedFile) int {
	total := 0
	for _, f := range ranked {
		total += len(f.Symbols)
	}
	return total
}

func renderHeader(mode string, files, symbols, usedTokens, budget int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "repository map · %s (%d files, %d symbols, ~%d tokens", mode, files, symbols, usedTokens)
	if budget > 0 {
		fmt.Fprintf(&b, ", budget %d", budget)
	}
	b.WriteString(")\n\n")
	return b.String()
}

// formatHeader renders one level-0 file line: path plus ranking annotations.
func formatHeader(f ranking.RankedFile) string {
	return f.Path + formatTags(f) + "\n"
}

// formatTags renders bracketed ranking annotations: entry point detection,
// importer counts (suppressed below two to hide single-importer noise),
// internal import counts, and untested packages.
func formatTags(f ranking.RankedFile) string {
	var tags []string
	if f.Tag == "entry" {
		tags = append(tags, "entry")
	}
	if f.ImportedBy >= 2 {
		tags = append(tags, fmt.Sprintf("imported by %d", f.ImportedBy))
	}
	if f.DependsOn > 0 {
		tags = append(tags, fmt.Sprintf("imports %d", f.DependsOn))
	}
	if !f.TestFile && !f.Tested && f.Language == languageGo {
		tags = append(tags, "untested")
	}
	if len(tags) == 0 {
		return ""
	}
	return " [" + strings.Join(tags, ", ") + "]"
}

// formatSummary renders one level-1 line: path plus per-kind symbol counts.
func formatSummary(f ranking.RankedFile) string {
	counts := kindSummary(f.Symbols)
	if counts == "" {
		return formatHeader(f)
	}
	return f.Path + formatTags(f) + " — " + counts + "\n"
}

func kindSummary(symbols []analyze.Symbol) string {
	counts := make(map[string]int, 4)
	for _, symbol := range symbols {
		counts[kindKeyword(symbol.Kind)]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if counts[key] == 1 {
			parts = append(parts, fmt.Sprintf("1 %s", singularKind(key)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %ss", counts[key], key))
	}
	return strings.Join(parts, ", ")
}

// keywordFunc and kindMethod are the two literals that recur across
// signature rendering; they are Go's declaration keyword and the snapshot's
// method symbol kind.
const (
	keywordFunc = "func"
	keywordType = "type"
	kindMethod  = "method"
)

// singularKind maps a plural kind word back to its singular form.
func singularKind(keyword string) string {
	if keyword == keywordFunc || keyword == keywordType || keyword == "const" || keyword == "var" {
		return keyword
	}
	return keyword
}

// formatEnrichedBlock renders one level-2 block in enriched mode: every
// exported symbol as a signature line with an optional doc subtitle.
func formatEnrichedBlock(f ranking.RankedFile) string {
	var b strings.Builder
	b.WriteString(formatHeader(f))
	for _, symbol := range f.Symbols {
		if !symbol.Exported {
			continue
		}
		b.WriteString("  " + formatSignature(symbol) + "\n")
		if symbol.Doc != "" {
			b.WriteString("    // " + symbol.Doc + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// formatCompactBlock renders one level-2 block in compact mode: exported
// symbol names only, grouped per kind keyword on single indented lines.
func formatCompactBlock(f ranking.RankedFile) string {
	var b strings.Builder
	b.WriteString(formatHeader(f))
	groups := make(map[string][]string, 4)
	var order []string
	for _, symbol := range f.Symbols {
		if !symbol.Exported {
			continue
		}
		key := kindKeyword(symbol.Kind)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], key+" "+symbol.Name)
	}
	slices.Sort(order)
	for _, key := range order {
		names := groups[key]
		slices.Sort(names)
		fmt.Fprintf(&b, "  %s\n", strings.Join(names, ", "))
	}
	b.WriteString("\n")
	return b.String()
}

// formatSignature renders one symbol declaration line: receiver-qualified
// methods, signatures on callables, and field lists on struct-like types.
func formatSignature(symbol analyze.Symbol) string {
	keyword := kindKeyword(symbol.Kind)
	switch symbol.Kind {
	case kindMethod:
		if symbol.Receiver != "" {
			return fmt.Sprintf("func (%s) %s%s", symbol.Receiver, symbol.Name, callSignature(symbol.Signature))
		}
		return fmt.Sprintf("func %s%s", symbol.Name, callSignature(symbol.Signature))
	case kindFunction:
		return fmt.Sprintf("func %s%s", symbol.Name, callSignature(symbol.Signature))
	case "struct", "interface", keywordType:
		if symbol.Signature != "" && symbol.Signature != "{}" {
			return fmt.Sprintf("type %s%s", symbol.Name, symbol.Signature)
		}
		return fmt.Sprintf("type %s", symbol.Name)
	default:
		return fmt.Sprintf("%s %s", keyword, symbol.Name)
	}
}

// callSignature prefixes a parameter list with a space only when the stored
// signature already carries one, so "(x int) error" renders as
// "Name(x int) error" without a doubled space.
func callSignature(signature string) string {
	if signature == "" {
		return ""
	}
	if strings.HasPrefix(signature, "(") {
		return signature
	}
	return " " + signature
}

func kindKeyword(kind string) string {
	switch kind {
	case kindFunction:
		return keywordFunc
	case kindMethod:
		return keywordFunc
	case "struct", "interface", keywordType:
		return "type"
	case "constant":
		return "const"
	case "variable":
		return "var"
	default:
		return kind
	}
}

// EnrichedCost estimates the rendered byte cost of one file's level-2 block
// under the enriched renderer. It must track formatEnrichedBlock within a
// small margin; see TestEnrichedCostMatchesRenderer.
func EnrichedCost(symbols []analyze.Symbol) int {
	cost := 0
	for _, s := range symbols {
		if !s.Exported {
			continue
		}
		cost += 8 + len(s.Name) + len(s.Signature)
		if s.Kind == kindMethod && s.Receiver != "" {
			cost += len(s.Receiver) + 4
		}
		if s.Doc != "" {
			cost += 8 + len(s.Doc)
		}
	}
	return cost
}

// CompactCost estimates the rendered byte cost of one file's level-2 block
// under the compact renderer; strictly lower than EnrichedCost for any file
// with exported symbols.
func CompactCost(symbols []analyze.Symbol) int {
	cost := 0
	for _, s := range symbols {
		if !s.Exported {
			continue
		}
		cost += 8 + len(s.Name) + len(s.Kind)
	}
	return cost
}

// estimateTokens approximates token count as ceil(UTF-8 bytes / 4).
func estimateTokens(text string) int { return (len(text) + 3) / 4 }
