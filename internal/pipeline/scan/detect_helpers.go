package scan

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// ─── fork helpers ─────────────────────────────────────────────────────────────

// Shared string literals used across fork/fanout detection. Centralizing
// them as constants keeps the repeated sentinel values single-sourced.
const (
	// conditionDefault is the fork-branch condition used for the default
	// (expression-less) case clause.
	conditionDefault = "default"
	// gateHTTPRoutes is the fanout gate label for HTTP handler registration.
	gateHTTPRoutes = "HTTP routes"
	// labelNotFound is the http.NotFound branch label excluded from route
	// fanout targets.
	labelNotFound = "NotFound"
	// gateSubcommand is the fanout gate label for Cobra AddCommand clusters.
	gateSubcommand = "subcommand"
	// literalGo is the bare "go" word shared by goroutine fanout target flags
	// and Go language tags.
	literalGo = "go"
)

// cleanCondition simplifies a raw Go expression into a human-readable label.
func cleanCondition(cond string) string {
	cond = strings.TrimSpace(cond)

	// Rule 4: keep "default" as-is.
	if cond == conditionDefault {
		return cond
	}

	// Rule 1: "x != nil" → "error" (if x is "err") or the variable name.
	if idx := strings.Index(cond, " != nil"); idx > 0 {
		v := strings.TrimSpace(cond[:idx])
		if v == "err" {
			return "error"
		}
		return v
	}

	// Rule 3: "st.Field" or "s.Field" → extract field name.
	if strings.HasPrefix(cond, "st.") || strings.HasPrefix(cond, "s.") {
		dot := strings.IndexByte(cond, '.')
		if dot >= 0 {
			rest := cond[dot+1:]
			if dot2 := strings.IndexByte(rest, '.'); dot2 > 0 {
				return rest[:dot2]
			}
			return rest
		}
	}

	// Rule 2: "&&" → take leftmost clause.
	if amp := strings.Index(cond, " && "); amp > 0 {
		left := cond[:amp]
		if len(left) < len(cond) {
			return cleanCondition(left) // recurse for chained cleanup
		}
	}

	// Rule 5: truncate to 40 chars.
	if len(cond) > 40 {
		return cond[:39] + "\u2026"
	}

	return cond
}

// dedupBranches merges branches with identical labels and removes noise.
func dedupBranches(branches []spec.Branch) []spec.Branch {
	// Check if there's any meaningful branch (label != "…").
	hasMeaningful := false
	for _, b := range branches {
		if b.Label != "\u2026" {
			hasMeaningful = true
			break
		}
	}

	// Group by label, combining conditions.
	labelMap := make(map[string][]string) // label → conditions
	var labelOrder []string
	for _, b := range branches {
		// Skip "…" branches when there's a meaningful one.
		if b.Label == "\u2026" && hasMeaningful {
			continue
		}
		conds := labelMap[b.Label]
		conds = append(conds, b.Condition)
		labelMap[b.Label] = conds
		if len(conds) == 1 {
			labelOrder = append(labelOrder, b.Label)
		}
	}

	var out []spec.Branch
	for _, label := range labelOrder {
		conds := labelMap[label]
		combined := strings.Join(conds, " / ")
		out = append(out, spec.Branch{Condition: combined, Label: label})
	}
	return out
}

// ─── shared helpers ───────────────────────────────────────────────────────────

// parseGoFiles parses the given relative paths under root.
// Files that fail to parse are silently skipped.
func parseGoFiles(root string, relPaths []string) ([]*ast.File, *token.FileSet) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, rel := range relPaths {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		abs := filepath.Join(root, rel)
		f, err := parser.ParseFile(fset, abs, nil, parser.ParseComments)
		if err != nil {
			continue // tolerate partial failures
		}
		files = append(files, f)
	}
	return files, fset
}

// collectPackageFiles returns all file paths from phases that share the same
// package directory prefix as the given phase. This lets cross-sub-phase
// lookups work after splitLargePhases splits a package into multiple sub-phases.
func collectPackageFiles(phase spec.Phase, allPhases []spec.Phase) []string {
	if len(phase.Files) == 0 {
		return nil
	}
	prefix := groupKey(phase.Files[0])

	seen := make(map[string]bool)
	var result []string
	for _, p := range allPhases {
		if len(p.Files) == 0 {
			continue
		}
		if groupKey(p.Files[0]) != prefix {
			continue
		}
		for _, f := range p.Files {
			if !seen[f] {
				seen[f] = true
				result = append(result, f)
			}
		}
	}
	return result
}

// exprString returns a best-effort string representation of an AST expression
// via go/printer. Falls back to "<expr>" on error.
func exprString(fset *token.FileSet, node ast.Node) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return "<expr>"
	}
	return buf.String()
}
