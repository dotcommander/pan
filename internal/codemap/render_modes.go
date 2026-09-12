package codemap

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// CallEvidence is one bounded call site for a selected exported symbol.
type CallEvidence struct {
	File       string `json:"file"`
	Symbol     string `json:"symbol"`
	Caller     string `json:"caller"`
	CallerFile string `json:"caller_file"`
	Line       int    `json:"line"`
	Confidence string `json:"confidence"`
}

const callEdgeKind = "calls"

func collectCallEvidence(ranked []ranking.RankedFile, opts Options) []CallEvidence {
	if !opts.Calls {
		return nil
	}
	targets := callTargets(ranked, opts.CallsThreshold)
	var out []CallEvidence
	for _, edge := range opts.Edges {
		if !callEdgeAllowed(edge.Kind, edge.Confidence, edge.Location.Path, opts.CallsIncludeTests) {
			continue
		}
		for _, file := range targets[edge.To] {
			out = append(out, CallEvidence{File: file.Path, Symbol: edge.To, Caller: edge.From, CallerFile: edge.Location.Path, Line: edge.Location.Line, Confidence: edge.Confidence})
		}
	}
	slices.SortFunc(out, func(a, b CallEvidence) int {
		if a.File != b.File {
			return strings.Compare(a.File, b.File)
		}
		if a.Symbol != b.Symbol {
			return strings.Compare(a.Symbol, b.Symbol)
		}
		if a.CallerFile != b.CallerFile {
			return strings.Compare(a.CallerFile, b.CallerFile)
		}
		return a.Line - b.Line
	})
	if opts.CallsLimit > 0 && len(out) > opts.CallsLimit {
		out = out[:opts.CallsLimit]
	}
	return out
}

func callTargets(ranked []ranking.RankedFile, threshold int) map[string][]ranking.RankedFile {
	if threshold == 0 {
		threshold = 2
	}
	targets := make(map[string][]ranking.RankedFile)
	for _, file := range ranked {
		if file.ImportedBy < threshold {
			continue
		}
		for _, symbol := range file.Symbols {
			if symbol.Exported {
				targets[symbol.Name] = append(targets[symbol.Name], file)
			}
		}
	}
	return targets
}

func callEdgeAllowed(kind, confidence, path string, includeTests bool) bool {
	return kind == callEdgeKind && confidence == analyze.ConfidenceConfirmed && (includeTests || !strings.HasSuffix(path, "_test.go"))
}

func renderBodyWithOptions(ranked []ranking.RankedFile, mode string, explain bool, calls []CallEvidence) string {
	var b strings.Builder
	for _, file := range ranked {
		switch file.DetailLevel {
		case -1:
			continue
		case 0:
			b.WriteString(formatHeader(file))
		case 1:
			b.WriteString(formatSummary(file))
		default:
			if mode == ModeCompact {
				b.WriteString(formatCompactBlock(file))
			} else {
				b.WriteString(formatEnrichedBlock(file))
			}
		}
		if explain {
			fmt.Fprintf(&b, "  [score %d: %s]\n", file.Score, scoreComponents(file))
		}
		for _, call := range calls {
			if call.File == file.Path {
				fmt.Fprintf(&b, "  caller %s:%d -> %s (%s)\n", call.CallerFile, call.Line, call.Symbol, call.Confidence)
			}
		}
	}
	return b.String()
}

func renderVerbose(ranked []ranking.RankedFile, detail, explain bool, calls []CallEvidence) string {
	var b strings.Builder
	for _, file := range ranked {
		b.WriteString(formatHeader(file))
		if detail {
			renderDetailedSymbols(&b, file)
		} else {
			renderSymbolGroups(&b, file)
		}
		if explain {
			fmt.Fprintf(&b, "  [score %d: %s]\n", file.Score, scoreComponents(file))
		}
		for _, call := range calls {
			if call.File == file.Path {
				fmt.Fprintf(&b, "  caller %s:%d -> %s (%s)\n", call.CallerFile, call.Line, call.Symbol, call.Confidence)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func renderDetailedSymbols(b *strings.Builder, file ranking.RankedFile) {
	for _, symbol := range file.Symbols {
		fmt.Fprintf(b, "  %s\n", formatSignature(symbol))
		if symbol.Doc != "" {
			fmt.Fprintf(b, "    // %s\n", symbol.Doc)
		}
	}
}

func renderSymbolGroups(b *strings.Builder, file ranking.RankedFile) {
	groups := make(map[string][]string)
	for _, symbol := range file.Symbols {
		key := kindKeyword(symbol.Kind)
		groups[key] = append(groups[key], symbol.Name)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		slices.Sort(groups[key])
		fmt.Fprintf(b, "  %s: %s\n", key, strings.Join(groups[key], ", "))
	}
}

func scoreComponents(file ranking.RankedFile) string {
	keys := make([]string, 0, len(file.Components))
	for key := range file.Components {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, file.Components[key]))
	}
	return strings.Join(parts, ",")
}

func renderLines(ranked []ranking.RankedFile, root string, tokens int) string {
	var b strings.Builder
	limit := tokens * 4
	for _, file := range ranked {
		var block strings.Builder
		block.WriteString(formatHeader(file))
		data, err := os.ReadFile(filepath.Join(root, file.Path))
		if err == nil {
			lines := strings.Split(string(data), "\n")
			for _, symbol := range file.Symbols {
				if !symbol.Exported || symbol.Location.Line < 1 || symbol.Location.Line > len(lines) {
					continue
				}
				fmt.Fprintf(&block, "| %s\n", strings.TrimSpace(lines[symbol.Location.Line-1]))
			}
		}
		block.WriteByte('\n')
		if limit > 0 && b.Len()+block.Len() > limit && b.Len() > 0 {
			break
		}
		b.WriteString(block.String())
	}
	return b.String()
}

func renderXML(ranked []ranking.RankedFile) string {
	type symbol struct {
		Name      string `xml:"name,attr"`
		Kind      string `xml:"kind,attr"`
		Line      int    `xml:"line,attr"`
		Signature string `xml:",chardata"`
	}
	type file struct {
		Path     string   `xml:"path,attr"`
		Language string   `xml:"lang,attr"`
		Score    int      `xml:"score,attr"`
		Symbols  []symbol `xml:"symbol"`
	}
	type document struct {
		XMLName         xml.Name `xml:"repomap"`
		Files           int      `xml:"files,attr"`
		Symbols         int      `xml:"symbols,attr"`
		SelectedFiles   int      `xml:"selected-files,attr"`
		SelectedSymbols int      `xml:"selected-symbols,attr"`
		OmittedFiles    int      `xml:"omitted-files,attr"`
		OmittedSymbols  int      `xml:"omitted-symbols,attr"`
		Entries         []file   `xml:"file"`
	}
	totalSymbols := countSymbols(ranked)
	doc := document{Files: len(ranked), Symbols: totalSymbols}
	for _, rf := range ranked {
		if rf.DetailLevel < 0 {
			doc.OmittedFiles++
			doc.OmittedSymbols += len(rf.Symbols)
			continue
		}
		entry := file{Path: rf.Path, Language: rf.Language, Score: rf.Score}
		for _, s := range rf.Symbols {
			entry.Symbols = append(entry.Symbols, symbol{Name: s.Name, Kind: s.Kind, Line: s.Location.Line, Signature: s.Signature})
		}
		doc.Entries = append(doc.Entries, entry)
	}
	doc.SelectedFiles = len(doc.Entries)
	doc.SelectedSymbols = totalSymbols - doc.OmittedSymbols
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ""
	}
	return xml.Header + string(data) + "\n"
}
