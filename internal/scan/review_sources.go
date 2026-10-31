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

// ReviewSourceOptions selects the same packets as the standalone scanners.
// Patterns applies the optional custom risk catalog to the built-in queue.
type ReviewSourceOptions struct {
	Limit    int
	Language string
	Patterns *CustomPatterns
}

type ReviewSourceReports struct {
	Risks   RiskReport
	Surface SurfaceReport
	Effects EffectsReport
}

// sourceLineView lives only for the current file. Reusing it never changes
// snapshot identity: the source is read live with the standalone scan bounds.
type sourceLineView struct {
	lines     []sourceLine
	truncated bool
}

// ReviewSources scans each admitted file once for all three audit packets.
// Source lines are released between files; standalone packet APIs keep their
// independent reads and error behavior.
func ReviewSources(ctx context.Context, snap analyze.Snapshot, opts ReviewSourceOptions) (ReviewSourceReports, error) {
	effectOpts := EffectsOptions{Limit: opts.Limit, Language: opts.Language}
	if err := effectOpts.Validate(); err != nil {
		return ReviewSourceReports{}, err
	}
	var custom []compiledPattern
	var customTerms []preparedCustomTerm
	if opts.Patterns != nil {
		customTerms = prepareCustomTerms(opts.Patterns.PathTerms)
		var err error
		custom, err = compileCustomPatterns(*opts.Patterns)
		if err != nil {
			return ReviewSourceReports{}, err
		}
	}
	structure := structuralSignals(snap)
	riskRules := riskTable{module: structure.module, terms: riskPathTermTable(), patterns: riskPatternTable(), importedBy: structure.importedBy, dependsOn: structure.dependsOn, symbols: structure.symbols, signatures: structure.signatures}
	surfacePatterns, effectPatterns := surfaceTable(), effectTable()
	result := ReviewSourceReports{Surface: SurfaceReport{Counts: map[string]int{}}}
	laneFiles := map[string][]string{}
	kindFiles := map[string][]string{}
	excluded := map[string]int{}
	considered, inspected, contentTruncated, baseScored := 0, 0, 0, 0
	for _, file := range snap.Files {
		class := fileclass.Classify(file.Path, file.Language, file.Generated)
		riskEligible := class == fileclass.Production
		otherEligible := file.Language != analyze.LanguageUnknown && !isTestPath(file.Path)
		effectEligible := otherEligible && (opts.Language == "" || opts.Language == file.Language)
		if !riskEligible {
			excluded[string(class)]++
		} else {
			considered++
			inspected++
		}
		if !riskEligible && !otherEligible {
			continue
		}
		lines, truncated, err := readLines(ctx, filepath.Join(snap.Root, filepathFromSlash(file.Path)))
		if err != nil {
			switch {
			case riskEligible:
				return ReviewSourceReports{}, fmt.Errorf("scan risk source %s: %w", file.Path, err)
			case otherEligible:
				return ReviewSourceReports{}, fmt.Errorf("scan surface source %s: %w", file.Path, err)
			}
		}
		source := &sourceLineView{lines: lines, truncated: truncated}
		if riskEligible {
			risk, err := riskForFileWithLines(ctx, snap, file, riskRules, source)
			if err != nil {
				return ReviewSourceReports{}, err
			}
			if slices.Contains(risk.Reasons, "scan:content_truncated") {
				contentTruncated++
			}
			if risk.Score != 0 {
				baseScored++
			}
			if opts.Patterns != nil {
				if err := addCustomRiskWithLines(ctx, snap.Root, file.Path, customRiskRules{customTerms, custom}, &risk, source); err != nil {
					return ReviewSourceReports{}, err
				}
			}
			if risk.Score != 0 || opts.Patterns != nil {
				result.Risks.Files = append(result.Risks.Files, risk)
				for _, lane := range risk.Lanes {
					laneFiles[lane] = append(laneFiles[lane], file.Path)
				}
			}
		}
		if otherEligible {
			addReviewSurface(&result.Surface, file.Path, source, surfacePatterns)
		}
		if effectEligible {
			scanned, err := scanEffectsFileWithLines(ctx, snap.Root, file, effectOpts, effectPatterns, source)
			if err != nil {
				return ReviewSourceReports{}, err
			}
			if scanned.ok {
				result.Effects.Files = append(result.Effects.Files, scanned.file)
				result.Effects.Truncations = append(result.Effects.Truncations, scanned.truncations...)
				for _, effect := range scanned.effects {
					kindFiles[effect.Kind] = append(kindFiles[effect.Kind], file.Path)
				}
			}
		}
	}
	finishReviewSources(&result, snap, opts, excluded, laneFiles, kindFiles, effectPatterns, considered, inspected, contentTruncated, baseScored)
	return result, nil
}

func addReviewSurface(report *SurfaceReport, path string, source *sourceLineView, patterns []surfacePattern) {
	hits := scanSurfaceLines(source.lines, patterns)
	if len(hits) == 0 {
		return
	}
	for _, hit := range hits {
		report.Counts[hit.Kind]++
	}
	report.Files = append(report.Files, SurfaceFile{Path: path, Hits: capHits(hits), Kinds: hitKinds(hits)})
	if len(hits) > perFileHitCap {
		report.Truncations = append(report.Truncations, Truncation{Field: "files[" + path + "].hits", Shown: perFileHitCap, Total: len(hits), Reason: "surface per-file cap"})
	}
	if source.truncated {
		report.Truncations = append(report.Truncations, Truncation{Field: "files[" + path + "].lines", Shown: len(source.lines), Total: len(source.lines) + 1, Reason: "line bound reached; tail not inspected"})
	}
}

func finishReviewSources(out *ReviewSourceReports, snap analyze.Snapshot, opts ReviewSourceOptions, excluded map[string]int, laneFiles, kindFiles map[string][]string, patterns []effectPattern, considered, inspected, truncated, baseScored int) {
	slices.SortFunc(out.Risks.Files, func(a, b FileRisk) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Path, b.Path)
	})
	riskTotal := len(out.Risks.Files)
	out.Risks.Lanes = buildRiskLanes(laneFiles)
	out.Risks.Analysis = RiskCoverage{Complete: snap.Status.Complete, Classes: defaultRiskOptions().IncludeClasses, SnapshotFiles: len(snap.Files), EligibleFiles: considered, InspectedContentFiles: inspected, ScoredFiles: riskTotal, ReturnedFiles: riskTotal, ContentTruncatedFiles: truncated, ExcludedByClass: excluded, SkippedCount: snap.Status.SkippedCount, Limits: snap.Status.Limits}
	if baseScored == 0 {
		out.Risks.FilesOmittedReason = "no files scored above the review threshold"
	}
	if opts.Patterns != nil {
		out.Risks = finalizeCustomRisk(out.Risks, opts.Limit)
	} else {
		if opts.Limit > 0 && riskTotal > opts.Limit {
			out.Risks.Files = out.Risks.Files[:opts.Limit]
			out.Risks.FilesOmittedReason = fmt.Sprintf("showing %d of %d scored files; truncated by --top", len(out.Risks.Files), riskTotal)
		}
		out.Risks.Analysis.ReturnedFiles = len(out.Risks.Files)
	}
	slices.SortFunc(out.Surface.Files, func(a, b SurfaceFile) int {
		if len(a.Hits) != len(b.Hits) {
			return len(b.Hits) - len(a.Hits)
		}
		return strings.Compare(a.Path, b.Path)
	})
	surfaceTotal := len(out.Surface.Files)
	if opts.Limit > 0 && surfaceTotal > opts.Limit {
		out.Surface.Files = out.Surface.Files[:opts.Limit]
	}
	switch {
	case len(out.Surface.Files) == 0:
		out.Surface.FilesOmittedReason = "no surface data extracted from scanned files"
	case len(out.Surface.Files) < surfaceTotal:
		out.Surface.FilesOmittedReason = fmt.Sprintf("showing %d of %d files; truncated by --top", len(out.Surface.Files), surfaceTotal)
	}
	slices.SortFunc(out.Effects.Files, func(a, b EffectFile) int {
		if len(a.Effects) != len(b.Effects) {
			return len(b.Effects) - len(a.Effects)
		}
		return strings.Compare(a.Path, b.Path)
	})
	effectTotal := len(out.Effects.Files)
	if opts.Limit > 0 && effectTotal > opts.Limit {
		out.Effects.Files = out.Effects.Files[:opts.Limit]
	}
	out.Effects.Kinds = buildEffectKinds(kindFiles, patterns)
	switch {
	case len(out.Effects.Files) == 0:
		out.Effects.FilesOmittedReason = "no side-effect data extracted from scanned files"
	case len(out.Effects.Files) < effectTotal:
		out.Effects.FilesOmittedReason = fmt.Sprintf("showing %d of %d files; truncated by --limit", len(out.Effects.Files), effectTotal)
	}
}
