package scan

import (
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// kindMethod is the symbols-table kind for method symbols.
const kindMethod = "method"

// stageCandidate is one symbol under consideration for a phase chip.
type stageCandidate struct {
	sym        symbols.Symbol
	importedBy int
	filePath   string // repo-relative path (from rf.Path)
}

// extractStages converts ranked file symbols into spec stages for one phase.
// Only exported functions and methods are included.
// Order: constructors (New…) first, then by ImportedBy descending, then by source line.
// The list is capped at maxStages; overflow count is returned as the second value
// so the caller can set Phase.TruncatedCount — "+N more" never materialises as a chip.
// For Boot phases with no exported symbols, falls back to main() if present.
func extractStages(files []symbols.RankedFile, maxStages int, kind spec.PhaseKind) ([]spec.Stage, int) {
	candidates := stageCandidates(files)
	if len(candidates) == 0 && kind == spec.PhaseKindBoot {
		candidates = bootFallbackCandidates(files)
	}
	candidates = dedupeCandidates(candidates)
	sortStageCandidates(candidates)
	return buildStages(candidates, maxStages, kind)
}

// stageCandidates collects the exported, non-utility function and method
// symbols from non-utility files.
func stageCandidates(files []symbols.RankedFile) []stageCandidate {
	var candidates []stageCandidate
	for _, rf := range files {
		// Skip utility-role files — they provide helpers, not pipeline steps.
		if isUtilityFile(rf.Path) {
			continue
		}
		for _, sym := range rf.Symbols {
			if eligibleStageSymbol(sym) {
				candidates = append(candidates, stageCandidate{sym: sym, importedBy: rf.ImportedBy, filePath: rf.Path})
			}
		}
	}
	return candidates
}

// eligibleStageSymbol reports whether a symbol can become a pipeline chip:
// a real (non-universe) identifier, exported, function/method/fn kind, and
// not generated plumbing, an accessor, or a utility symbol.
func eligibleStageSymbol(sym symbols.Symbol) bool {
	if !token.IsIdentifier(sym.Name) || types.Universe.Lookup(sym.Name) != nil {
		return false
	}
	if !sym.Exported {
		return false
	}
	if sym.Kind != "function" && sym.Kind != kindMethod && sym.Kind != "fn" {
		return false
	}
	// Skip utility symbols by name — suppressed regardless of which file they land in.
	if isUtilitySymbol(sym.Name) {
		return false
	}
	// Skip marshal/unmarshal methods — generated plumbing, not UX steps.
	if matchesAny(sym.Name, "Marshal", "Unmarshal") {
		return false
	}
	// Skip predicate/accessor methods — helpers, not pipeline stages.
	if sym.Kind == kindMethod && matchesAny(sym.Name, "Is", "Has", "Get", "As", "String", "Single") {
		return false
	}
	// Skip zero-arg methods that return a single value (pure getters regardless of name length).
	if sym.Kind == kindMethod && sym.ParamCount == 0 && sym.ResultCount == 1 {
		return !matchesAny(sym.Name, "Run", "New", "Scan", "Serve", "Handle", "Listen")
	}
	return true
}

// bootFallbackCandidates collects receiverless main() functions from the
// files as the entry-point chip for a Boot phase with no exported symbols.
func bootFallbackCandidates(files []symbols.RankedFile) []stageCandidate {
	var candidates []stageCandidate
	for _, rf := range files {
		for _, sym := range rf.Symbols {
			if sym.Name == mainIdent && sym.Kind == "function" {
				candidates = append(candidates, stageCandidate{sym: sym, importedBy: rf.ImportedBy, filePath: rf.Path})
			}
		}
	}
	return candidates
}

// dedupeCandidates keeps one candidate per symbol name — the one with the
// higher importedBy count.
func dedupeCandidates(candidates []stageCandidate) []stageCandidate {
	seen := make(map[string]int) // name → index in deduped
	var deduped []stageCandidate
	for _, c := range candidates {
		if idx, exists := seen[c.sym.Name]; exists {
			if c.importedBy > deduped[idx].importedBy {
				deduped[idx] = c
			}
			continue
		}
		seen[c.sym.Name] = len(deduped)
		deduped = append(deduped, c)
	}
	return deduped
}

// sortStageCandidates orders candidates constructors-first, then by
// importedBy descending, then by source line.
func sortStageCandidates(candidates []stageCandidate) {
	slices.SortFunc(candidates, func(a, b stageCandidate) int {
		aNew := isConstructor(a.sym.Name)
		bNew := isConstructor(b.sym.Name)
		if aNew != bNew {
			if aNew {
				return -1
			}
			return 1
		}
		if b.importedBy != a.importedBy {
			return b.importedBy - a.importedBy
		}
		return a.sym.Line - b.sym.Line
	})
}

// buildStages caps the candidate list at maxStages and renders each
// surviving candidate as a chip stage. The overflow count is returned so
// the caller can set Phase.TruncatedCount.
func buildStages(candidates []stageCandidate, maxStages int, kind spec.PhaseKind) ([]spec.Stage, int) {
	truncated := 0
	if len(candidates) > maxStages {
		truncated = len(candidates) - maxStages
		candidates = candidates[:maxStages]
	}

	stages := make([]spec.Stage, 0, len(candidates))
	for _, c := range candidates {
		chip := &spec.Chip{
			Label:      c.sym.Name,
			Style:      chipStyle(c.sym.Name),
			SourceFile: c.filePath,
			SourceLine: c.sym.Line,
		}
		if shouldBeWide(c.sym) {
			chip.Wide = true
		}
		chip.Subtitle = chipSubtitle(c.sym)
		// main() in a Boot phase gets a boot style and entry-point subtitle.
		if c.sym.Name == mainIdent && kind == spec.PhaseKindBoot {
			chip.Style = spec.ChipStyleBoot
			chip.Subtitle = "entry point"
		}
		stages = append(stages, spec.Stage{Chip: chip})
	}
	return stages, truncated
}

// isConstructor returns true if the symbol name starts with "New".
func isConstructor(name string) bool {
	return strings.HasPrefix(name, "New")
}

// chipStyle maps a symbol name to a ChipStyle.
func chipStyle(name string) spec.ChipStyle {
	switch {
	case isConstructor(name):
		return spec.ChipStyleBoot
	case matchesAny(name, "Handle", "Serve", "Run", "Start", "Listen", "Write", "Send", "Emit", "Render"):
		return spec.ChipStyleIO
	case matchesAny(name, "Validate", "Check", "Verify", "Assert", "Ensure"):
		return spec.ChipStyleGate
	default:
		return spec.ChipStyleDefault
	}
}

// shouldBeWide returns true for heavyweight stages that should span extra width.
func shouldBeWide(sym symbols.Symbol) bool {
	if sym.LineSpan() > 50 {
		return true
	}
	if sym.ParamCount >= 4 {
		return true
	}
	// Large functions with heavyweight-pattern names.
	return sym.LineSpan() > 20 && matchesAny(sym.Name, "Dispatch", "Process", "Execute", "Handle", "Collect", "Build", "Resolve")
}

// chipSubtitle derives a concise subtitle from a symbol's signature and receiver.
// Returns "" if no meaningful subtitle can be produced.
func chipSubtitle(sym symbols.Symbol) string {
	sig := strings.TrimSpace(sym.Signature)
	if sig == "" || sig == "()" || sig == "{}" {
		return ""
	}
	condensed := condenseSignature(sig)
	// If the receiver adds context, prepend it.
	if sym.Receiver != "" {
		prefix := sym.Receiver + " · "
		if len(prefix)+len(condensed) <= 55 {
			condensed = prefix + condensed
		}
	}
	// If too long, fall back to stat form.
	if len(condensed) > 50 {
		condensed = signatureStatsFallback(sym, sig)
	}
	return condensed
}

// condenseSignature renders a signature as "ctx · provider · opts → *Agent".
func condenseSignature(sig string) string {
	condensed := strings.ReplaceAll(sig, ", ", " · ")
	condensed = strings.Replace(condensed, ") ", " → ", 1)
	// Strip leading "(" if present after transform.
	condensed = strings.TrimPrefix(condensed, "(")
	// Strip trailing ")" if no return type.
	condensed = strings.TrimSuffix(condensed, ")")
	// Strip orphaned parens from multi-return types like "(int, error)".
	condensed = strings.ReplaceAll(condensed, "(", "")
	return strings.ReplaceAll(condensed, ")", "")
}

// signatureStatsFallback renders an over-long signature as parameter and
// result counts, keeping the receiver prefix when present.
func signatureStatsFallback(sym symbols.Symbol, sig string) string {
	parts := []string{}
	if sym.ParamCount > 0 {
		parts = append(parts, fmt.Sprintf("%d params", sym.ParamCount))
	}
	if sym.ResultCount > 0 {
		parts = append(parts, resultSummary(sym, sig))
	}
	if len(parts) == 0 {
		return ""
	}
	condensed := strings.Join(parts, " ")
	if sym.Receiver != "" {
		condensed = sym.Receiver + " · " + condensed
	}
	return condensed
}

// resultSummary summarizes a single-result signature with its return type
// when extractable, or result counts otherwise.
func resultSummary(sym symbols.Symbol, sig string) string {
	if sym.ResultCount != 1 {
		return fmt.Sprintf("→ %d results", sym.ResultCount)
	}
	if idx := strings.LastIndex(sig, ") "); idx >= 0 {
		return "→ " + strings.TrimSpace(sig[idx+2:])
	}
	return "→ 1 result"
}
