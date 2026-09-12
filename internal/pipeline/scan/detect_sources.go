package scan

import (
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// ─── detectSources ────────────────────────────────────────────────────────────

// detectSources populates Phase.Sources by scanning import paths for known
// external-boundary packages (HTTP clients, database drivers, message queues).
// It uses AST parsing of each phase's Go files to extract import declarations.
func detectSources(root string, phases []spec.Phase) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)

	// Known source-signal import prefixes mapped to human-readable labels.
	// Loaded from data/source_signals.yaml (embedded) and overlaid with
	// ~/.config/Pan/source_signals.yaml if present. See source_signals.go.
	signals := sourceSignals()

	for pi, phase := range out {
		files, _ := parseGoFiles(root, phase.Files)
		if len(files) == 0 {
			continue
		}

		seen := make(map[string]bool)
		for _, f := range files {
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for signal, label := range signals {
					if strings.HasPrefix(path, signal) && !seen[label] {
						seen[label] = true
						out[pi].Sources = append(out[pi].Sources, label)
					}
				}
			}
		}
		// Sort for deterministic output.
		if len(out[pi].Sources) > 0 {
			slices.Sort(out[pi].Sources)
		}
	}

	return out
}
