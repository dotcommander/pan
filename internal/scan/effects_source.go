package scan

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dotcommander/pan/internal/analyze"
)

func scanEffectsFile(ctx context.Context, root string, file analyze.File, options EffectsOptions, patterns []effectPattern) (scannedEffectsFile, error) {
	return scanEffectsFileWithLines(ctx, root, file, options, patterns, nil)
}

func scanEffectsFileWithLines(ctx context.Context, root string, file analyze.File, options EffectsOptions, patterns []effectPattern, source *sourceLineView) (scannedEffectsFile, error) {
	if file.Language == analyze.LanguageUnknown || isTestPath(file.Path) || (options.Language != "" && file.Language != options.Language) {
		return scannedEffectsFile{}, nil
	}
	if source == nil {
		lines, truncated, err := readLines(ctx, filepath.Join(root, filepathFromSlash(file.Path)))
		if err != nil {
			return scannedEffectsFile{}, fmt.Errorf("scan effects source %s: %w", file.Path, err)
		}
		source = &sourceLineView{lines: lines, truncated: truncated}
	}
	effects := scanEffects(file.Language, source.lines, patterns)
	if options.Kind != "" {
		effects = filterEffectsByKind(effects, options.Kind)
	}
	if len(effects) == 0 {
		return scannedEffectsFile{}, nil
	}
	for index := range effects {
		effects[index].Path = file.Path
		if effects[index].Lane == "" {
			effects[index].Lane = effectKindLane(effects[index].Kind, patterns)
		}
	}
	effects = normalizeEffects(effects)
	lanes := effectLanes(effects, patterns)
	effectFile := EffectFile{ID: "pan:effect:" + effectSlug(file.Path), Path: file.Path, Score: effectScore(effects), EvidenceClass: effectEvidenceClass(lanes), Confidence: effectConfidence(lanes), Lanes: lanes, Effects: effects}
	var truncations []Truncation
	if len(effects) > perFileHitCap {
		effectFile.Effects = effects[:perFileHitCap:perFileHitCap]
		truncations = append(truncations, Truncation{Field: "files[" + file.Path + "].effects", Shown: perFileHitCap, Total: len(effects), Reason: "effects per-file cap"})
	}
	if source.truncated {
		truncations = append(truncations, Truncation{Field: "files[" + file.Path + "].lines", Shown: len(source.lines), Total: len(source.lines) + 1, Reason: "line bound reached; tail not inspected"})
	}
	return scannedEffectsFile{file: effectFile, effects: effects, truncations: truncations, ok: true}, nil
}
