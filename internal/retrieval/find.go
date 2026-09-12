// Package retrieval answers symbol-, file-, route-, and task-oriented
// queries over one finalized analysis snapshot and its ranking. Every result
// carries evidence labels describing how each fact was derived, and every
// capped surface carries a truncation record, so consumers can always tell
// confirmed structure from lexical coincidence and complete evidence from
// bounded output.
package retrieval

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// Match bases reported on SymbolMatch, strongest first.
const (
	BasisExact          = "exact"
	BasisCaseFold       = "case-insensitive"
	BasisPrefix         = "prefix"
	BasisContains       = "contains"
	BasisHandle         = "handle"
	maxAmbiguousMatches = 5
)

// SymbolMatch is one hit from Find. File plus Symbol.Location identify the
// hit; Handle is the stable reference accepted back by Find.
type SymbolMatch struct {
	File        string         `json:"file"`
	Symbol      analyze.Symbol `json:"symbol"`
	Handle      string         `json:"handle"`
	Score       int            `json:"score"`
	Basis       string         `json:"basis"`
	FileScore   int            `json:"file_score"`
	DetailLevel int            `json:"detail_level"`
}

// FindQuery is a parsed symbol query: a name plus optional kind and file
// filters.
type FindQuery struct {
	Name string
	Kind string
	File string
}

// ParseFindQuery splits a positional query of the form
//
//	kind:func:file:cmd/main.go:main
//
// into name, kind, and file filters. Qualifier prefixes may appear in either
// order and consume exactly two colon-separated tokens each; the remaining
// tokens rejoin as the name. A query carrying the "symbol:" handle prefix is
// returned whole as the name so Find can resolve it.
func ParseFindQuery(query string) FindQuery {
	query = strings.TrimSpace(query)
	if query == "" {
		return FindQuery{}
	}
	if strings.HasPrefix(query, "symbol:") {
		return FindQuery{Name: query}
	}
	parts := strings.Split(query, ":")
	var parsed FindQuery
	for len(parts) > 1 {
		switch parts[0] {
		case "kind":
			parsed.Kind = parts[1]
			parts = parts[2:]
		case "file":
			parsed.File = parts[1]
			parts = parts[2:]
		default:
			parsed.Name = strings.Join(parts, ":")
			return parsed
		}
	}
	parsed.Name = parts[0]
	return parsed
}

// Find searches the ranked symbol set. name is required; an empty name
// returns no matches. kind filters case-insensitively on Symbol.Kind; file
// filters by substring on the owning file path. A name carrying the "symbol:"
// handle prefix resolves to its single exact match. Matches sort by score
// (exact 100 > case-insensitive 75 > prefix 50 > contains 25), then by owning
// file rank, then by path.
func Find(ranked []ranking.RankedFile, name, kind, file string) []SymbolMatch {
	if handle, ok := ParseSymbolHandle(name); ok {
		return findByHandle(ranked, handle)
	}
	if name == "" {
		return nil
	}
	nameLower := strings.ToLower(name)
	kindLower := strings.ToLower(kind)
	var matches []SymbolMatch
	for _, rf := range ranked {
		if file != "" && !strings.Contains(rf.Path, file) {
			continue
		}
		for _, symbol := range rf.Symbols {
			if kindLower != "" && strings.ToLower(symbol.Kind) != kindLower {
				continue
			}
			score, basis := scoreMatch(symbol.Name, name, nameLower)
			if score == 0 {
				continue
			}
			matches = append(matches, SymbolMatch{
				File:        rf.Path,
				Symbol:      symbol,
				Handle:      SymbolHandle(rf.Path, symbol),
				Score:       score,
				Basis:       basis,
				FileScore:   rf.Score,
				DetailLevel: rf.DetailLevel,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].FileScore != matches[j].FileScore {
			return matches[i].FileScore > matches[j].FileScore
		}
		return matches[i].File < matches[j].File
	})
	return matches
}

func findByHandle(ranked []ranking.RankedFile, handle SymbolHandleParts) []SymbolMatch {
	for _, rf := range ranked {
		if rf.Path != handle.File {
			continue
		}
		for _, symbol := range rf.Symbols {
			if symbol.Name == handle.Name && symbol.Kind == handle.Kind && symbol.Location.Line == handle.Line {
				return []SymbolMatch{{
					File:        rf.Path,
					Symbol:      symbol,
					Handle:      SymbolHandle(rf.Path, symbol),
					Score:       100,
					Basis:       BasisHandle,
					FileScore:   rf.Score,
					DetailLevel: rf.DetailLevel,
				}}
			}
		}
	}
	return nil
}

func scoreMatch(symbolName, name, nameLower string) (int, string) {
	switch {
	case symbolName == name:
		return 100, BasisExact
	case strings.EqualFold(symbolName, name):
		return 75, BasisCaseFold
	case strings.HasPrefix(symbolName, name):
		return 50, BasisPrefix
	case strings.Contains(strings.ToLower(symbolName), nameLower):
		return 25, BasisContains
	}
	return 0, ""
}

// SymbolHandle returns a stable handle for one symbol occurrence, or "" when
// the inputs cannot identify one.
func SymbolHandle(file string, symbol analyze.Symbol) string {
	if file == "" || symbol.Name == "" || symbol.Location.Line <= 0 {
		return ""
	}
	return fmt.Sprintf("symbol:%s::%s#%s@%d", file, symbol.Name, symbol.Kind, symbol.Location.Line)
}

// SymbolHandleParts is one parsed "symbol:" handle: the owning file, the
// symbol name and kind, and the declaration line.
type SymbolHandleParts struct {
	File string
	Name string
	Kind string
	Line int
}

// ParseSymbolHandle parses handles emitted by SymbolHandle. ok is false
// when handle is not a well-formed symbol handle.
func ParseSymbolHandle(handle string) (SymbolHandleParts, bool) {
	body, found := strings.CutPrefix(handle, "symbol:")
	if !found {
		return SymbolHandleParts{}, false
	}
	filePart, rest, found := strings.Cut(body, "::")
	if !found || filePart == "" {
		return SymbolHandleParts{}, false
	}
	namePart, linePart, found := strings.Cut(rest, "@")
	if !found {
		return SymbolHandleParts{}, false
	}
	name, kind, found := strings.Cut(namePart, "#")
	if !found || name == "" || kind == "" {
		return SymbolHandleParts{}, false
	}
	line, err := strconv.Atoi(linePart)
	if err != nil || line <= 0 {
		return SymbolHandleParts{}, false
	}
	return SymbolHandleParts{File: filePart, Name: name, Kind: kind, Line: line}, true
}

// ambiguousMatches returns up to maxAmbiguousMatches alternative matches
// following the primary hit, plus a truncation record when more existed.
func ambiguousMatches(matches []SymbolMatch) ([]SymbolMatch, *analyze.Truncation) {
	if len(matches) <= 1 {
		return nil, nil
	}
	rest := matches[1:]
	if len(rest) <= maxAmbiguousMatches {
		return rest, nil
	}
	return rest[:maxAmbiguousMatches], &analyze.Truncation{
		Field:  "ambiguous",
		Shown:  maxAmbiguousMatches,
		Total:  len(rest),
		Reason: "ambiguity cap",
	}
}

// isTestFile reports whether path is a Go test file.
func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

// sortedUniqueStrings returns a sorted, deduplicated copy.
func sortedUniqueStrings(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return slices.Compact(out)
}
