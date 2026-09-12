package retrieval

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// Risk levels for one file's blast radius.
const (
	RiskHigh   = "high"
	RiskMedium = "medium"
	RiskLow    = "low"
)

// FileSummary identifies one ranked file in impact output.
type FileSummary struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Package  string `json:"package,omitempty"`
	Score    int    `json:"score"`
	TestFile bool   `json:"test_file,omitempty"`
}

// ImpactResult is the deterministic blast-radius summary for one file:
// who consumes it, which tests likely cover it, how risky a change looks,
// and what to inspect next.
type ImpactResult struct {
	File               FileSummary        `json:"file"`
	Imports            []string           `json:"imports,omitempty"`
	ImportedBy         []string           `json:"imported_by,omitempty"`
	Tests              []string           `json:"tests,omitempty"`
	ExportedSymbols    []analyze.Symbol   `json:"exported_symbols,omitempty"`
	Boundaries         []string           `json:"boundaries,omitempty"`
	ScoreComponents    map[string]int     `json:"score_components,omitempty"`
	ParseMethod        string             `json:"parse_method,omitempty"`
	RiskLevel          string             `json:"risk_level"`
	AffectedPackages   []string           `json:"affected_packages,omitempty"`
	CheckNext          []string           `json:"check_next,omitempty"`
	LikelyTestCommands []string           `json:"likely_test_commands,omitempty"`
	ReadNext           []analyze.ReadNext `json:"read_next,omitempty"`
	OmittedReason      string             `json:"omitted_reason,omitempty"`
	Evidence           map[string]string  `json:"evidence,omitempty"`
}

// Impact summarizes the blast radius of the file at relPath within ranked.
// The second return is false when relPath is not part of the ranking.
func Impact(ranked []ranking.RankedFile, relPath string) (ImpactResult, bool) {
	relPath = path.Clean(relPath)
	target, ok := rankedByPath(ranked, relPath)
	if !ok {
		return ImpactResult{}, false
	}
	importers := impactImporters(target, ranked)
	tests := impactTests(target.Path, ranked)
	result := ImpactResult{
		File:               fileSummary(target),
		Imports:            slices.Clone(target.Imports),
		ImportedBy:         importers,
		Tests:              tests,
		ExportedSymbols:    exportedSymbols(target.Symbols),
		ScoreComponents:    maps.Clone(target.Components),
		ParseMethod:        parseMethod(target),
		RiskLevel:          impactRiskLevel(target, importers, tests),
		AffectedPackages:   affectedPackages(target, importers, ranked),
		CheckNext:          impactCheckNext(importers, tests),
		LikelyTestCommands: likelyTestCommands(target.Language, tests),
		ReadNext:           impactReadNext(target, importers, tests, ranked),
		OmittedReason:      impactOmittedReason(importers, tests),
		Evidence: map[string]string{
			"imports":     analyze.ConfidenceSyntactic,
			"imported_by": analyze.ConfidenceSyntactic,
			"tests":       analyze.ConfidenceHeuristic,
			"risk_level":  analyze.ConfidenceHeuristic,
		},
	}
	return result, true
}

func rankedByPath(ranked []ranking.RankedFile, relPath string) (ranking.RankedFile, bool) {
	for _, rf := range ranked {
		if path.Clean(rf.Path) == relPath {
			return rf, true
		}
	}
	return ranking.RankedFile{}, false
}

func fileSummary(rf ranking.RankedFile) FileSummary {
	return FileSummary{Path: rf.Path, Language: rf.Language, Package: rf.Package, Score: rf.Score, TestFile: rf.TestFile}
}

// impactImporters lists files whose internal imports resolve to the target's
// package directory. Resolution is structural when the module path is known
// and suffix-based otherwise; either way the underlying import edges come
// from parsed import declarations, so the label is syntactic.
func impactImporters(target ranking.RankedFile, ranked []ranking.RankedFile) []string {
	dir := path.Dir(target.Path)
	var importers []string
	for _, rf := range ranked {
		if rf.Path == target.Path {
			continue
		}
		if slices.Contains(rf.InternalImports, dir) {
			importers = append(importers, rf.Path)
		}
	}
	return importers
}

// impactTests lists test files that plausibly exercise the target: same
// directory with a name derived from the target's base name, or a test file
// whose path starts with the target's stem. Name-shape matching only, hence
// the heuristic evidence label.
func impactTests(targetPath string, ranked []ranking.RankedFile) []string {
	dir := path.Dir(targetPath)
	base := strings.TrimSuffix(path.Base(targetPath), path.Ext(targetPath))
	stem := strings.TrimSuffix(targetPath, path.Ext(targetPath))
	var tests []string
	for _, rf := range ranked {
		if !isTestFile(rf.Path) {
			continue
		}
		testBase := strings.TrimSuffix(path.Base(rf.Path), path.Ext(rf.Path))
		if strings.HasPrefix(rf.Path, stem) || (path.Dir(rf.Path) == dir && strings.Contains(testBase, base)) {
			tests = append(tests, rf.Path)
		}
	}
	return tests
}

func exportedSymbols(symbols []analyze.Symbol) []analyze.Symbol {
	var out []analyze.Symbol
	for _, symbol := range symbols {
		if symbol.Exported {
			out = append(out, symbol)
		}
	}
	return out
}

// impactRiskLevel classifies change risk from importer fan-in, test
// presence, and exported surface size.
func impactRiskLevel(target ranking.RankedFile, importers, tests []string) string {
	exported := exportedSymbols(target.Symbols)
	switch {
	case len(importers) > 10:
		return RiskHigh
	case len(importers) > 3:
		return RiskMedium
	case len(tests) == 0 && len(exported) > 0:
		return RiskMedium
	default:
		return RiskLow
	}
}

func affectedPackages(target ranking.RankedFile, importers []string, ranked []ranking.RankedFile) []string {
	packages := make(map[string]struct{}, len(importers)+1)
	if target.Package != "" {
		packages[target.Package] = struct{}{}
	}
	for _, importer := range importers {
		if rf, ok := rankedByPath(ranked, importer); ok && rf.Package != "" {
			packages[rf.Package] = struct{}{}
		}
	}
	out := make([]string, 0, len(packages))
	for pkg := range packages {
		out = append(out, pkg)
	}
	slices.Sort(out)
	return out
}

// impactCheckNext lists bounded next actions: up to three importers, then
// likely tests, up to five entries total.
func impactCheckNext(importers, tests []string) []string {
	var out []string
	for _, file := range importers {
		out = append(out, "inspect importer "+file)
		if len(out) >= 3 {
			break
		}
	}
	for _, file := range tests {
		out = append(out, "run or inspect likely test "+file)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func likelyTestCommands(language string, tests []string) []string {
	if len(tests) == 0 || language != languageGo {
		return nil
	}
	dirs := make(map[string]struct{}, len(tests))
	for _, test := range tests {
		dir := path.Dir(test)
		if dir == "." {
			dirs["./"] = struct{}{}
			continue
		}
		dirs["./"+dir] = struct{}{}
	}
	out := make([]string, 0, len(dirs))
	for dir := range dirs {
		out = append(out, "go test "+dir)
	}
	slices.Sort(out)
	return out
}

// impactReadNext suggests bounded inspection spans: the target's first
// exported symbol, then importers and likely tests, capped at five entries.
func impactReadNext(target ranking.RankedFile, importers, tests []string, ranked []ranking.RankedFile) []analyze.ReadNext {
	var items []analyze.ReadNext
	for _, symbol := range target.Symbols {
		if !symbol.Exported || symbol.Location.Line <= 0 {
			continue
		}
		items = append(items, readNextSpan(target.Path, symbol.Location.Line, symbolEnd(symbol), "inspect exported symbol "+symbol.Name))
		break
	}
	if len(items) == 0 {
		items = append(items, readNextSpan(target.Path, 1, 1, "inspect target file"))
	}
	for _, file := range importers {
		items = append(items, readNextSpan(file, firstSymbolLine(ranked, file), firstSymbolLine(ranked, file), "inspect importer before changing target"))
		if len(items) >= 3 {
			break
		}
	}
	for _, file := range tests {
		line := firstSymbolLine(ranked, file)
		items = append(items, readNextSpan(file, line, line, "inspect likely test coverage"))
		if len(items) >= 5 {
			break
		}
	}
	items = dedupeReadNext(items, 5)
	return items
}

func firstSymbolLine(ranked []ranking.RankedFile, file string) int {
	rf, ok := rankedByPath(ranked, file)
	if !ok {
		return 1
	}
	for _, symbol := range rf.Symbols {
		if symbol.Location.Line > 0 {
			return symbol.Location.Line
		}
	}
	return 1
}

func symbolEnd(symbol analyze.Symbol) int {
	if symbol.EndLine >= symbol.Location.Line {
		return symbol.EndLine
	}
	return symbol.Location.Line
}

func readNextSpan(file string, start, end int, note string) analyze.ReadNext {
	return analyze.ReadNext{Path: file, Start: start, End: end, Note: note}
}

func dedupeReadNext(items []analyze.ReadNext, cap int) []analyze.ReadNext {
	seen := make(map[string]struct{}, len(items))
	var out []analyze.ReadNext
	for _, item := range items {
		key := fmt.Sprintf("%s:%d:%d:%s", item.Path, item.Start, item.End, item.Note)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
		if len(out) >= cap {
			break
		}
	}
	return out
}

func impactOmittedReason(importers, tests []string) string {
	if len(importers) > 3 || len(tests) > 2 {
		return "check_next and read_next are capped to keep impact output bounded"
	}
	return ""
}
