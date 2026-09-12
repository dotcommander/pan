package scan

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// subtitleFuncInfo holds the extracted data for a single function.
type subtitleFuncInfo struct {
	doc  string // first sentence of doc comment
	body *ast.BlockStmt
	fset *token.FileSet
}

// enrichSubtitles walks each phase's Go files and overlays doc-comment
// subtitles onto chips and fork branches whose existing signature-based
// subtitle is absent or too long to fit in the rendered chip.
// Cross-package branch targets are resolved via a lazily built global
// function map.
func enrichSubtitles(root string, phases []spec.Phase) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)

	// globalFuncs is built lazily the first time a branch label is not found
	// in the local package map. This handles cross-package fork targets (e.g.,
	// a fork in cmd/foo that dispatches to functions in internal/foo).
	var globalFuncs map[string]subtitleFuncInfo

	for pi, phase := range out {
		pkgFiles := collectPackageFiles(phase, out)
		files, fset := parseGoFiles(root, pkgFiles)
		if len(files) == 0 {
			continue
		}
		funcs := buildSubtitleMap(files, fset)
		enrichPhaseSubtitles(&out[pi], funcs, &globalFuncs, root, out)
	}

	return out
}

// buildSubtitleMap indexes one parsed file set by function name, capturing
// each body plus the first sentence of its doc comment.
func buildSubtitleMap(files []*ast.File, fset *token.FileSet) map[string]subtitleFuncInfo {
	funcs := make(map[string]subtitleFuncInfo)
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			info := subtitleFuncInfo{body: fn.Body, fset: fset}
			if fn.Doc != nil {
				info.doc = docFirstSentence(fn.Doc.Text(), fn.Name.Name)
			}
			funcs[fn.Name.Name] = info
		}
	}
	return funcs
}

// enrichPhaseSubtitles overlays doc-comment subtitles onto one phase's chip
// and fork-branch stages, falling back to the global function map for
// cross-package branch targets.
func enrichPhaseSubtitles(phase *spec.Phase, funcs map[string]subtitleFuncInfo, globalFuncs *map[string]subtitleFuncInfo, root string, phases []spec.Phase) {
	for si, stage := range phase.Stages {
		if stage.Chip != nil {
			if info, ok := funcs[stage.Chip.Label]; ok {
				stage.Chip.Subtitle = bestSubtitle(stage.Chip.Subtitle, info.doc)
			}
		}
		if stage.Fork != nil {
			enrichBranchSubtitles(stage.Fork, funcs, globalFuncs, root, phases)
		}
		phase.Stages[si] = stage
	}
}

// enrichBranchSubtitles overlays doc-comment subtitles onto one fork's
// branches, resolving labels missing from the local map through the lazily
// built global function map.
func enrichBranchSubtitles(fork *spec.Fork, funcs map[string]subtitleFuncInfo, globalFuncs *map[string]subtitleFuncInfo, root string, phases []spec.Phase) {
	for bi, branch := range fork.Branches {
		info, ok := funcs[branch.Label]
		if !ok {
			// Local package miss — try the global function map as fallback.
			if *globalFuncs == nil {
				*globalFuncs = buildGlobalFuncMap(root, phases)
			}
			info, ok = (*globalFuncs)[branch.Label]
			if !ok {
				continue
			}
		}
		fork.Branches[bi].Subtitle = bestSubtitle(branch.Subtitle, info.doc)
	}
}

// buildGlobalFuncMap parses every file referenced by any phase and returns
// a map of function name → subtitleFuncInfo. Used as a fallback for branch
// subtitle resolution when the branch target lives in a different package
// directory than the fork (cross-package dispatch).
//
// First occurrence wins so that the most-specific (earlier-phase) definition
// is preferred when the same name appears in multiple packages.
func buildGlobalFuncMap(root string, phases []spec.Phase) map[string]subtitleFuncInfo {
	result := make(map[string]subtitleFuncInfo)

	// Collect every unique file across all phases.
	seen := make(map[string]bool)
	var allFiles []string
	for _, p := range phases {
		for _, f := range p.Files {
			if !seen[f] && !strings.HasSuffix(f, "_test.go") {
				seen[f] = true
				allFiles = append(allFiles, f)
			}
		}
	}

	files, fset := parseGoFiles(root, allFiles)
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if _, exists := result[name]; exists {
				continue // first occurrence wins
			}
			info := subtitleFuncInfo{body: fn.Body, fset: fset}
			if fn.Doc != nil {
				info.doc = docFirstSentence(fn.Doc.Text(), name)
			}
			result[name] = info
		}
	}

	return result
}

// bestSubtitle decides whether to override the current signature-based subtitle
// with a doc excerpt. Rule: keep the current subtitle when it fits (≤60 chars);
// otherwise fall back to doc when it is substantive (≥20 chars); otherwise keep
// current unchanged (even if empty or long). Stdlib-call chains are never used
// as subtitles — they add noise without structural information.
func bestSubtitle(current, doc string) string {
	if current != "" && len(current) <= 60 {
		return current
	}
	if len(doc) >= 20 {
		return doc
	}
	return current
}

// docFirstSentence extracts the first sentence from a Go doc comment.
// Returns "" if the doc is empty or the first sentence is too long (>60 chars).
// Strips the function name prefix if the doc starts with it (Go convention).
func docFirstSentence(text string, funcName string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// Take first line or first sentence (up to period).
	line := text
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		line = text[:idx]
	}
	if idx := strings.IndexByte(line, '.'); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)

	// Strip conventional "FuncName " prefix.
	// e.g., "Load reads the YAML spec from disk" → "reads the YAML spec from disk"
	if strings.HasPrefix(line, funcName+" ") {
		line = line[len(funcName)+1:]
	}

	// Too long or too short — not useful.
	if len(line) > 60 || len(line) < 5 {
		return ""
	}

	return line
}
