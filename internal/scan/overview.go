package scan

import (
	"path"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// LanguageCount groups inventoried files by detected language.
type LanguageCount struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
}

// OverviewReport summarizes repository shape from one analysis snapshot.
type OverviewReport struct {
	Files          int             `json:"files"`
	Symbols        int             `json:"symbols"`
	Edges          int             `json:"edges"`
	Instructions   []string        `json:"instructions"`
	Languages      []LanguageCount `json:"languages"`
	GeneratedFiles int             `json:"generated_files"`
	TestFiles      int             `json:"test_files"`
	GoPackages     []string        `json:"go_packages"`
}

// Overview derives summary counts from a finalized snapshot. The result is
// deterministic: languages are sorted by name and package paths deduplicated
// and sorted.
func Overview(snap analyze.Snapshot) OverviewReport {
	report := OverviewReport{
		Files:          len(snap.Files),
		Symbols:        len(snap.Symbols),
		Edges:          len(snap.Edges),
		Instructions:   snap.Instructions,
		Languages:      languageCounts(snap.Files),
		GeneratedFiles: countGenerated(snap.Files),
		TestFiles:      countTests(snap.Files),
		GoPackages:     goPackages(snap.Symbols),
	}
	if report.Instructions == nil {
		report.Instructions = []string{}
	}
	return report
}

func languageCounts(files []analyze.File) []LanguageCount {
	counts := make(map[string]int, 8)
	for _, file := range files {
		counts[file.Language]++
	}
	out := make([]LanguageCount, 0, len(counts))
	for language, count := range counts {
		out = append(out, LanguageCount{Language: language, Files: count})
	}
	slices.SortFunc(out, func(a, b LanguageCount) int {
		if a.Files != b.Files {
			return b.Files - a.Files
		}
		if a.Language != b.Language {
			return strings.Compare(a.Language, b.Language)
		}
		return 0
	})
	return out
}

func countGenerated(files []analyze.File) int {
	total := 0
	for _, file := range files {
		if file.Generated {
			total++
		}
	}
	return total
}

func countTests(files []analyze.File) int {
	total := 0
	for _, file := range files {
		if isTestPath(file.Path) {
			total++
		}
	}
	return total
}

// goPackages returns the distinct package directories that contributed
// symbols, sorted and deduplicated.
func goPackages(symbols []analyze.Symbol) []string {
	seen := make(map[string]struct{}, len(symbols))
	out := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		dir := path.Dir(symbol.Location.Path)
		if dir == "." || dir == "/" {
			dir = ""
		}
		if dir == "" {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	slices.Sort(out)
	return out
}
