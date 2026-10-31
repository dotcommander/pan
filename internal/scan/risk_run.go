package scan

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/fileclass"
)

func filepathFromSlash(rel string) string {
	return filepath.FromSlash(rel)
}

// Risk ranks non-test, non-generated files by deterministic path, content,
// and structural signals. Content is inspected only for Go files already
// admitted by the snapshot bounds. top > 0 caps the returned file list;
// lanes always reflect every scored file.
func Risk(ctx context.Context, snap analyze.Snapshot, top int) (RiskReport, error) {
	return RiskWithOptions(ctx, snap, top, defaultRiskOptions())
}

// RiskWithOptions ranks admitted files using deterministic path, structural,
// and language-aware content signals.
func RiskWithOptions(ctx context.Context, snap analyze.Snapshot, top int, options RiskOptions) (RiskReport, error) {
	structure := structuralSignals(snap)
	table := riskTable{
		module:     structure.module,
		terms:      riskPathTermTable(),
		patterns:   riskPatternTable(),
		importedBy: structure.importedBy,
		dependsOn:  structure.dependsOn,
		symbols:    structure.symbols,
		signatures: structure.signatures,
	}
	var files []FileRisk
	laneFiles := map[string][]string{}
	considered := 0
	excluded := map[string]int{}
	inspected, contentTruncated := 0, 0
	for _, file := range snap.Files {
		class := fileclass.Classify(file.Path, file.Language, file.Generated)
		if !options.admits(class) {
			excluded[string(class)]++
			continue
		}
		considered++
		inspected++
		risk, err := riskForFile(ctx, snap, file, table)
		if err != nil {
			return RiskReport{}, err
		}
		if slices.Contains(risk.Reasons, "scan:content_truncated") {
			contentTruncated++
		}
		if risk.Score == 0 {
			continue
		}
		files = append(files, risk)
		for _, lane := range risk.Lanes {
			laneFiles[lane] = append(laneFiles[lane], risk.Path)
		}
	}
	slices.SortFunc(files, func(a, b FileRisk) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Path, b.Path)
	})
	total := len(files)
	if top > 0 && len(files) > top {
		files = files[:top]
	}
	report := RiskReport{Files: files, Lanes: buildRiskLanes(laneFiles), Analysis: RiskCoverage{Complete: snap.Status.Complete, Classes: options.IncludeClasses, SnapshotFiles: len(snap.Files), EligibleFiles: considered, InspectedContentFiles: inspected, ScoredFiles: total, ReturnedFiles: len(files), ContentTruncatedFiles: contentTruncated, ExcludedByClass: excluded, SkippedCount: snap.Status.SkippedCount, Limits: snap.Status.Limits}}
	switch {
	case len(files) == 0:
		report.FilesOmittedReason = "no files scored above the review threshold"
	case len(files) < total:
		report.FilesOmittedReason = fmt.Sprintf("showing %d of %d scored files; truncated by --top", len(files), total)
	}
	return report, nil
}

// riskForFile scores one file from the entrypoint, path-term, structural,
// and content signals in table.
func riskForFile(ctx context.Context, snap analyze.Snapshot, file analyze.File, table riskTable) (FileRisk, error) {
	return riskForFileWithLines(ctx, snap, file, table, nil)
}

func riskForFileWithLines(ctx context.Context, snap analyze.Snapshot, file analyze.File, table riskTable, source *sourceLineView) (FileRisk, error) {
	lower := strings.ToLower(file.Path)
	risk := FileRisk{Path: file.Path, FileClass: string(fileclass.Classify(file.Path, file.Language, file.Generated)), Confidence: evidenceHeuristic}
	seen := map[string]int{}
	add := func(points int, lane, reason string) {
		matches := seen[reason]
		seen[reason]++
		if matches >= 1 {
			points >>= matches
			if points == 0 {
				points = 1
			}
		}
		risk.Score += points
		risk.Lanes = appendUnique(risk.Lanes, lane)
		if matches == 0 {
			risk.Reasons = append(risk.Reasons, reason)
			risk.ScoreComponents = append(risk.ScoreComponents, RiskScoreComponent{ID: lane + ":" + reason, Lane: lane, EvidenceKind: "heuristic", Confidence: evidenceHeuristic, Points: points, Matches: 1, TotalMatches: 1, CountedMatches: 1, Reason: reason})
		} else {
			for i := range risk.ScoreComponents {
				if risk.ScoreComponents[i].Reason == reason {
					risk.ScoreComponents[i].Matches++
					risk.ScoreComponents[i].TotalMatches++
					risk.ScoreComponents[i].CountedMatches++
					risk.ScoreComponents[i].Points += points
					break
				}
			}
		}
	}
	addEntrypointRisk(file.Path, lower, add)
	addPathTermRisk(lower, table.terms, add)
	if file.Language == analyze.LanguageGo && risk.FileClass == string(fileclass.Production) {
		addStructuralRisk(file.Path, table, &risk, add)
	}
	patterns := table.patterns
	if file.Language != analyze.LanguageGo {
		patterns = genericRiskPatterns(table.patterns)
	}
	var truncated bool
	var err error
	if source == nil {
		truncated, err = addContentRisk(ctx, snap.Root, file.Path, patterns, riskContentRecorder(&risk, add))
	} else {
		truncated = source.truncated
		addContentRiskLines(source.lines, patterns, riskContentRecorder(&risk, add))
	}
	if err != nil {
		return FileRisk{}, err
	}
	if truncated {
		risk.Reasons = append(risk.Reasons, "scan:content_truncated")
	}
	risk.Reasons = capReasons(risk.Reasons)
	risk.ReviewPriority = risk.Score
	return risk, nil
}

// addStructuralRisk scores fan-in, fan-out, and symbol-density thresholds,
// recording the observed counts on the risk.
func addStructuralRisk(rel string, table riskTable, risk *FileRisk, add func(points int, lane, reason string)) {
	importPath := packageImportPathForModule(table.module, rel)
	if importedBy := table.importedBy[importPath]; importedBy >= importedByThreshold {
		risk.ImportedBy = importedBy
		add(importedByWeight, "architecture", fmt.Sprintf("package imported by %d files", importedBy))
	}
	if deps := table.dependsOn[rel]; deps >= dependsOnThreshold {
		risk.DependsOn = deps
		add(dependsOnWeight, "coupling", fmt.Sprintf("imports %d dependencies", deps))
	}
	if symbols := table.symbols[rel]; symbols >= symbolsThreshold {
		risk.Symbols = symbols
		add(symbolsWeight, "large-functions", fmt.Sprintf("%d symbols in one file", symbols))
	}
	if signatures := table.signatures[rel]; signatures > 0 {
		add(6*signatures, laneAPIContracts, fmt.Sprintf("%d complex public signature(s)", signatures))
	}
}

// addContentRisk scores bounded content patterns and reports whether the
// read hit the line bound.
func addContentRisk(ctx context.Context, root, rel string, patterns []riskPattern, add func(points int, lane, reason string, line int, text string, counted bool)) (bool, error) {
	lines, truncated, err := readLines(ctx, filepath.Join(root, filepathFromSlash(rel)))
	if err != nil {
		return false, fmt.Errorf("scan risk source %s: %w", rel, err)
	}
	addContentRiskLines(lines, patterns, add)
	return truncated, nil
}

func addContentRiskLines(lines []sourceLine, patterns []riskPattern, add func(int, string, string, int, string, bool)) {
	matches := map[string]int{}
	for _, line := range lines {
		for _, pattern := range patterns {
			if !pattern.Re.MatchString(line.text) || lineExcluded(pattern, line.text) {
				continue
			}
			matches[pattern.ID]++
			add(pattern.Weight, pattern.Lane, pattern.Reason, line.number, line.text, matches[pattern.ID] <= pattern.MaxMatches)
		}
	}
}
