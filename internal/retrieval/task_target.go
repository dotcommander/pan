package retrieval

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// buildTaskTarget assembles one target's bounded evidence and relationships.
// Every applied cap appends a truncation record.
func buildTaskTarget(snap analyze.Snapshot, ranked []ranking.RankedFile, candidate taskCandidate, consumed []string) (TaskTarget, []analyze.Truncation) {
	file := candidate.file
	var truncations []analyze.Truncation
	symbols := slices.Clone(file.Symbols)
	slices.SortStableFunc(symbols, func(a, b analyze.Symbol) int {
		if a.Exported != b.Exported {
			if a.Exported {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(symbols) > taskSymbolCap {
		truncations = append(truncations, analyze.Truncation{Field: "targets[" + file.Path + "].symbols", Shown: taskSymbolCap, Total: len(symbols), Reason: "symbol cap"})
		symbols = symbols[:taskSymbolCap]
	}
	importers := impactImporters(file, ranked)
	tests := impactTests(file.Path, ranked)
	var relations []TaskRelationship
	for _, importer := range importers {
		relations = append(relations, TaskRelationship{Kind: "consumer", Path: importer, Provenance: analyze.ConfidenceSyntactic})
	}
	for _, test := range tests {
		relations = append(relations, TaskRelationship{Kind: "test", Path: test, Provenance: analyze.ConfidenceHeuristic})
	}
	for _, caller := range lexicalCallerFiles(snap, file) {
		relations = append(relations, TaskRelationship{Kind: "caller", Path: caller, Provenance: analyze.ConfidenceLexical})
	}
	slices.SortStableFunc(relations, func(a, b TaskRelationship) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Path, b.Path)
	})
	consumers := capTaskStrings(importers, taskConsumerCap, "targets["+file.Path+"].consumers", &truncations)
	limitedTests := capTaskStrings(tests, taskTestCap, "targets["+file.Path+"].tests", &truncations)
	imports := capTaskStrings(file.Imports, taskImportCap, "targets["+file.Path+"].imports", &truncations)
	if len(relations) > taskRelationCap {
		truncations = append(truncations, analyze.Truncation{Field: "targets[" + file.Path + "].relationships", Shown: taskRelationCap, Total: len(relations), Reason: "relationship cap"})
		relations = relations[:taskRelationCap]
	}
	if candidate.evidenceTotal > len(candidate.evidence) {
		truncations = append(truncations, analyze.Truncation{Field: "targets[" + file.Path + "].evidence", Shown: len(candidate.evidence), Total: candidate.evidenceTotal, Reason: "evidence cap"})
	}
	target := TaskTarget{
		Path:       file.Path,
		Package:    file.Package,
		Confidence: taskConfidence(candidate),
		Symbols:    symbols,
		Evidence:   candidate.evidence,
		Relations:  relations,
		Consumers:  consumers,
		Tests:      limitedTests,
		Imports:    imports,
		Risk:       impactRiskLevel(file, importers, tests),
		Parse:      parseMethod(file),
		Consumed:   slices.Contains(consumed, file.Path),
	}
	if !target.Consumed {
		target.Source = taskSource(snap, file.Path, symbols, &truncations)
	}
	return target, truncations
}

func taskConfidence(candidate taskCandidate) string {
	switch {
	case candidate.fallback:
		return "fallback"
	case candidate.relevance >= 16:
		return "high"
	case candidate.relevance >= 8:
		return "medium"
	default:
		return "low"
	}
}

func capTaskStrings(values []string, cap int, field string, truncations *[]analyze.Truncation) []string {
	values = sortedUniqueStrings(values)
	if len(values) > cap {
		*truncations = append(*truncations, analyze.Truncation{Field: field, Shown: cap, Total: len(values), Reason: "relationship cap"})
		return values[:cap]
	}
	return values
}

func taskSource(snap analyze.Snapshot, relPath string, symbols []analyze.Symbol, truncations *[]analyze.Truncation) []TaskSource {
	source, ok := snap.Source(relPath)
	if !ok {
		return nil
	}
	var out []TaskSource
	for _, symbol := range symbols {
		if symbol.Location.Line <= 0 {
			continue
		}
		lines, truncated, _ := readSymbolSource(source, symbol, taskSourceLines)
		if len(lines) == 0 {
			continue
		}
		if truncated {
			span := symbolEnd(symbol) - symbol.Location.Line + 1
			*truncations = append(*truncations, analyze.Truncation{
				Field: "targets[" + relPath + "].source[" + symbol.Name + "]",
				Shown: len(lines), Total: span, Reason: "60 lines per symbol cap",
			})
		}
		out = append(out, TaskSource{Symbol: symbol.Name, Lines: lines})
		break
	}
	return out
}

func taskReadNext(targets []TaskTarget) []analyze.ReadNext {
	var items []analyze.ReadNext
	for _, target := range targets {
		for _, symbol := range target.Symbols {
			if symbol.Location.Line <= 0 {
				continue
			}
			items = append(items, readNextSpan(target.Path, symbol.Location.Line, symbolEnd(symbol), "inspect target symbol "+symbol.Name))
			break
		}
		for _, test := range target.Tests {
			items = append(items, readNextSpan(test, 1, 1, "inspect likely test coverage for "+target.Path))
		}
		if len(items) >= taskReadNextCap {
			break
		}
	}
	return dedupeReadNext(items, taskReadNextCap)
}

func taskVerifyCommands(targets []TaskTarget) []string {
	dirs := make(map[string]struct{})
	for _, target := range targets {
		for _, test := range target.Tests {
			if !strings.HasSuffix(test, ".go") {
				continue
			}
			dir := path.Dir(test)
			if dir == "." {
				dirs["./"] = struct{}{}
				continue
			}
			dirs["./"+dir] = struct{}{}
		}
	}
	out := make([]string, 0, len(dirs))
	for dir := range dirs {
		out = append(out, "go test "+dir)
	}
	slices.Sort(out)
	return out
}

func taskSourceOmission(path string, total int, reason string) analyze.Truncation {
	return analyze.Truncation{Field: fmt.Sprintf("targets[%s].source", path), Shown: 0, Total: total, Reason: reason}
}
