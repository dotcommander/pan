package retrieval

import (
	"bufio"
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

const (
	// defaultMaxSourceLines bounds the source excerpt returned per symbol.
	defaultMaxSourceLines = 200
	// maxSourceLineBytes bounds one source line; longer lines are still
	// returned but the scanner never allocates unbounded buffers.
	maxSourceLineBytes = 256 * 1024
	// maxCallers bounds the lexical caller list.
	maxCallers = 10
)

// SymbolOptions shapes one symbol-context query.
type SymbolOptions struct {
	// Kind and File filter the symbol search (same semantics as Find).
	Kind string
	File string
	// MaxSourceLines caps the source excerpt; <= 0 uses the default.
	MaxSourceLines int
	// Calls includes lexical callers. CallsIncludeTests adds _test.go callers;
	// CallsLimit caps them when positive, while zero leaves the list unbounded.
	Calls             bool
	CallsIncludeTests bool
	CallsLimit        int
}

// SourceLine is one line of extracted source.
type SourceLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// Caller is one lexical call site reaching the queried symbol.
type Caller struct {
	Symbol     string           `json:"symbol"`
	Location   analyze.Location `json:"location"`
	Confidence string           `json:"confidence"`
}

// SymbolResult is the bounded, symbol-centered bundle: the best match,
// ambiguous alternatives, lexical callers, a truncated source excerpt, the
// file's blast radius, and read-next spans.
type SymbolResult struct {
	Query           string               `json:"query"`
	Match           SymbolMatch          `json:"match"`
	Ambiguous       []SymbolMatch        `json:"ambiguous,omitempty"`
	Callers         []Caller             `json:"callers,omitempty"`
	Source          []SourceLine         `json:"source,omitempty"`
	SourceTruncated bool                 `json:"source_truncated,omitempty"`
	SourceNote      string               `json:"source_note,omitempty"`
	ReadNext        []analyze.ReadNext   `json:"read_next,omitempty"`
	Impact          ImpactResult         `json:"impact"`
	Truncations     []analyze.Truncation `json:"truncations,omitempty"`
}

// SymbolContext resolves query against ranked and returns the bounded
// context bundle for the best match. It reports an error when nothing
// matches; callers turn that into exit-code-1 usage feedback.
func SymbolContext(snap analyze.Snapshot, ranked []ranking.RankedFile, query string, opts SymbolOptions) (SymbolResult, error) {
	parsed := ParseFindQuery(query)
	if opts.Kind == "" {
		opts.Kind = parsed.Kind
	}
	if opts.File == "" {
		opts.File = parsed.File
	}
	if opts.MaxSourceLines <= 0 {
		opts.MaxSourceLines = defaultMaxSourceLines
	}
	matches := Find(ranked, parsed.Name, opts.Kind, opts.File)
	if len(matches) == 0 {
		return SymbolResult{}, fmt.Errorf("symbol %q not found", query)
	}
	match := matches[0]
	impact, _ := Impact(ranked, match.File)
	out := SymbolResult{
		Query:  query,
		Match:  match,
		Impact: impact,
	}
	if ambiguous, truncation := ambiguousMatches(matches); len(ambiguous) > 0 {
		out.Ambiguous = ambiguous
		if truncation != nil {
			out.Truncations = append(out.Truncations, *truncation)
		}
	}
	if opts.Calls {
		out.Callers = symbolCallers(snap, match, opts.CallsIncludeTests, opts.CallsLimit)
	}
	source, ok := snap.Source(match.File)
	if !ok {
		out.SourceNote = "captured source unavailable"
	} else {
		out.Source, out.SourceTruncated, out.SourceNote = readSymbolSource(source, match.Symbol, opts.MaxSourceLines)
	}
	if out.SourceTruncated {
		out.Truncations = append(out.Truncations, analyze.Truncation{
			Field:  "source",
			Shown:  len(out.Source),
			Total:  symbolSpan(match.Symbol),
			Reason: fmt.Sprintf("%d line source cap", opts.MaxSourceLines),
		})
	}
	out.ReadNext = []analyze.ReadNext{readNextSpan(match.File, match.Symbol.Location.Line, symbolEnd(match.Symbol), "inspect the matched symbol before editing")}
	return out, nil
}

func symbolSpan(symbol analyze.Symbol) int {
	if symbol.EndLine >= symbol.Location.Line {
		return symbol.EndLine - symbol.Location.Line + 1
	}
	return 1
}

// symbolCallers keeps resolved calls on their exact declaration. Lexical
// fallback belongs to a symbol only when its name is unique in the snapshot.
func symbolCallers(snap analyze.Snapshot, match SymbolMatch, includeTests bool, limit int) []Caller {
	uniqueName := true
	for _, symbol := range snap.Symbols {
		if symbol.Name == match.Symbol.Name && (symbol.Location != match.Symbol.Location || symbol.Kind != match.Symbol.Kind) {
			uniqueName = false
			break
		}
	}
	seen := make(map[string]struct{})
	var callers []Caller
	for _, edge := range snap.Edges {
		if edge.Kind != edgeKindCalls || edge.To != match.Symbol.Name {
			continue
		}
		confidence := analyze.ConfidenceLexical
		switch {
		case edge.Target != nil:
			if edge.Confidence != analyze.ConfidenceConfirmed || !edge.CallsSymbol(match.Symbol) {
				continue
			}
			confidence = analyze.ConfidenceConfirmed
		case !uniqueName || edge.Confidence != analyze.ConfidenceLexical:
			continue
		}
		if edge.From == match.Symbol.Name && edge.Location.Path == match.File && edge.Location.Line >= match.Symbol.Location.Line &&
			(match.Symbol.EndLine < match.Symbol.Location.Line || edge.Location.Line <= match.Symbol.EndLine) {
			continue // direct recursion is not caller evidence
		}
		if !includeTests && strings.Contains(edge.Location.Path, "_test.go") {
			continue
		}
		key := edge.From + "\x00" + edge.Location.Path + "\x00" + strconv.Itoa(edge.Location.Line) + ":" + strconv.Itoa(edge.Location.Column)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		callers = append(callers, Caller{Symbol: edge.From, Location: edge.Location, Confidence: confidence})
	}
	slices.SortFunc(callers, func(a, b Caller) int {
		if a.Location.Path != b.Location.Path {
			return compare(a.Location.Path, b.Location.Path)
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line - b.Location.Line
		}
		return a.Location.Column - b.Location.Column
	})
	if limit == 0 {
		return callers
	}
	if limit < 0 {
		limit = maxCallers
	}
	if len(callers) > limit {
		callers = callers[:limit]
	}
	return callers
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// readSymbolSource extracts the symbol's source lines from disk, capped at
// maxLines. The returned note explains why source may be missing or short.
func readSymbolSource(source []byte, symbol analyze.Symbol, maxLines int) (lines []SourceLine, truncated bool, note string) {
	if symbol.Location.Line <= 0 {
		return nil, false, "source span unavailable"
	}
	start, end := symbol.Location.Line, symbolEnd(symbol)
	if end-start+1 > maxLines {
		end = start + maxLines - 1
		truncated = true
	}
	scanner := bufio.NewScanner(bytes.NewReader(source))
	scanner.Buffer(make([]byte, 0, 64*1024), maxSourceLineBytes)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < start {
			continue
		}
		if lineNo > end {
			break
		}
		lines = append(lines, SourceLine{Number: lineNo, Text: scanner.Text()})
	}
	if err := scanner.Err(); err != nil {
		return lines, true, fmt.Sprintf("source truncated: %v", err)
	}
	return lines, truncated, ""
}
