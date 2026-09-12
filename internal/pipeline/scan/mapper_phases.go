package scan

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// keyInternalSpec is the group key of the dedicated spec-package phase.
const keyInternalSpec = "internal/spec"

// keySuppress marks utility files excluded from pipeline phases entirely.
const keySuppress = "__suppress__"

// groupPhases groups ranked files into phases by package path structure, then
// extracts stages from their exported symbols.
//
// Grouping rules:
//
//	cmd/<name>/  → Boot phase
//	internal/<pkg>/ → phase named after pkg
//	pkg/<pkg>/   → phase named after pkg
//	root .go files → phase named after module base
func groupPhases(ranked []symbols.RankedFile, root string, cfg Config) []spec.Phase {
	groupMap, order := assignGroups(ranked, root)
	slices.SortFunc(order, func(a, b string) int {
		return groupSortOrder(a) - groupSortOrder(b)
	})

	phases := make([]spec.Phase, 0, len(order))
	for _, key := range order {
		phases = append(phases, buildPhase(key, groupMap[key], root, cfg))
	}

	// Merge down to maxPhases if needed.
	if len(phases) > cfg.MaxPhases {
		phases = mergePhases(phases, cfg.MaxPhases)
	}
	return phases
}

// assignGroups maps every non-test ranked file to its group key, returning
// the file groups plus their first-seen order.
func assignGroups(ranked []symbols.RankedFile, root string) (map[string][]symbols.RankedFile, []string) {
	groupMap := make(map[string][]symbols.RankedFile)
	var order []string // first-seen order
	for _, rf := range ranked {
		rel := relPath(root, rf.Path)
		if strings.HasSuffix(rel, "_test.go") {
			continue // skip test files — they inflate stage counts without adding UX value
		}
		key := specGroupKey(rel)
		if key == keySuppress {
			continue // skip utility files entirely
		}
		if _, exists := groupMap[key]; !exists {
			order = append(order, key)
		}
		groupMap[key] = append(groupMap[key], rf)
	}
	return groupMap, order
}

// baseValidate is the filename base that becomes its own spec sub-phase.
const baseValidate = "validate"

// specGroupKey refines the group key for spec-package files, splitting
// internal/spec into sub-phases by filename semantics: validate becomes its
// own group, utilities are suppressed, and load.go/spec.go stay on the spec
// key to become the Load phase.
func specGroupKey(rel string) string {
	key := groupKey(rel)
	if key != keyInternalSpec {
		return key
	}
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(rel), ".go"), "_test")
	switch base {
	case baseValidate:
		return "internal/spec/validate"
	case "paths", "dirs":
		return keySuppress
	default:
		return key
	}
}

// buildPhase derives one pipeline phase from a file group.
func buildPhase(key string, files []symbols.RankedFile, root string, cfg Config) spec.Phase {
	filePaths := make([]string, 0, len(files))
	var allSymbols []symbols.Symbol
	for _, rf := range files {
		filePaths = append(filePaths, relPath(root, rf.Path))
		allSymbols = append(allSymbols, rf.Symbols...)
	}
	sort.Strings(filePaths)

	kind := inferKind(key, allSymbols)
	stages, truncated := extractStages(files, cfg.MaxStages, kind)
	return spec.Phase{
		Name:           phaseName(key, kind, root),
		Kind:           kind,
		Description:    kindDescription(kind),
		Files:          filePaths,
		Stages:         stages,
		TruncatedCount: truncated,
	}
}

// phaseName derives the display name for a file group, using well-known
// names for boot and dispatch phases and the module base for root files.
func phaseName(key string, kind spec.PhaseKind, root string) string {
	name := capitalize(pkgBaseName(key))
	if name == "" {
		name = filepath.Base(root)
	}
	if kind == spec.PhaseKindBoot {
		return "Boot"
	}
	if kind == spec.PhaseKindRoute && (strings.Contains(key, "commands") || strings.Contains(key, commandWord)) {
		return phaseNameDispatch
	}
	return name
}

// groupKey extracts a two-component prefix from a relative file path.
//
//	"cmd/Pan/main.go"         → "cmd/Pan"
//	"internal/serve/server.go"     → "internal/serve"
//	"pkg/util/util.go"             → "pkg/util"
//	"main.go"                      → ""
func groupKey(rel string) string {
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
	switch len(parts) {
	case 1:
		return "" // root-level file
	case 2:
		return parts[0] // single-component dir (rare)
	default:
		return parts[0] + "/" + parts[1]
	}
}

// groupSortOrder assigns a sort priority to a group key.
// Lower value = earlier in the output.
//
// Well-known pipeline packages get explicit priorities so that mergePhases
// cannot collapse them when the pre-merge phase count exceeds MaxPhases:
//   - internal/spec   →  9  (Parse phase, before generic internals)
//   - internal/render →  9  (Render phase, peers with spec in the data-flow layer)
func groupSortOrder(key string) int {
	switch {
	case key == "":
		return 40 // root files last
	case strings.HasPrefix(key, "cmd/"):
		return 0
	case strings.Contains(key, "commands") || strings.Contains(key, commandWord):
		return 5 // dispatch/route phases right after boot
	case key == keyInternalSpec || key == "internal/render":
		return 9 // Parse/Render phases — before generic internal/* at 10
	case strings.HasSuffix(key, "/validate"):
		return 11 // just after internal/ (10), before pkg/ (20)
	case strings.HasPrefix(key, "internal/"):
		return 10
	case strings.HasPrefix(key, "pkg/"):
		return 20
	default:
		return 30
	}
}

// pkgBaseName returns the last path component of a group key (e.g. "serve" from "internal/serve").
// Returns "" for the root group.
// Special cases:
//   - "cmd/<name>" → "<name>" (binary name, not "cmd")
//   - "internal/commands" or "internal/command" → "Commands"
func pkgBaseName(key string) string {
	if key == "" {
		return ""
	}
	// cmd/<name> groups: return the binary basename directly.
	if strings.HasPrefix(key, "cmd/") {
		return filepath.Base(key)
	}
	// internal/commands groups: use a fixed name to avoid deriving from file names.
	base := filepath.Base(key)
	if base == "commands" || base == commandWord {
		return "Commands"
	}
	return base
}

// inferKind returns a phase Kind based on the package base name. It
// falls back to PhaseKindUnknown when no match is found. The match table
// is loaded from data/kind_table.yaml (embedded) and overlaid with
// ~/.config/Pan/kind_table.yaml if present. See kind_table.go.
func inferKind(pkg string, _ []symbols.Symbol) spec.PhaseKind {
	// cmd/<name>/ groups always boot — the base name is the binary name, not "cmd".
	if strings.HasPrefix(strings.ToLower(pkg), "cmd/") {
		return spec.PhaseKindBoot
	}
	base := strings.ToLower(pkgBaseName(pkg))
	if base == "" {
		return spec.PhaseKindUnknown
	}
	for _, row := range kindTableRows() {
		if base == row.Match || strings.HasPrefix(base, row.Match) {
			return spec.PhaseKind(row.Kind)
		}
	}
	return spec.PhaseKindUnknown
}

// kindDescription returns a short human-readable description for a phase Kind.
// Returns "" for unknown kinds so the Description field omitempty stays clean.
func kindDescription(kind spec.PhaseKind) string {
	switch kind {
	case spec.PhaseKindUnknown:
		return ""
	case spec.PhaseKindBoot:
		return "entry point and CLI bootstrap"
	case spec.PhaseKindRoute:
		return "command dispatch and subcommand routing"
	case spec.PhaseKindParse:
		return "data loading and deserialization"
	case spec.PhaseKindCheck:
		return "validation and structural checks"
	case spec.PhaseKindRender:
		return "template rendering and HTML output"
	case spec.PhaseKindServe:
		return "HTTP request handling"
	case spec.PhaseKindWatch:
		return "filesystem change detection"
	case spec.PhaseKindEmit:
		return "output delivery (stdout, stderr, or file)"
	case spec.PhaseKindAnalyze:
		return "source code analysis"
	default:
		return ""
	}
}
