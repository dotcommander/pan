package scan

import (
	"go/ast"
	"go/token"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// detectForks walks each phase's Go files and replaces chip stages whose label
// matches a top-level function containing a dominant control-flow construct
// (switch, if/else-if chain, or select with 2+ arms) with a Fork stage.
//
// Only the first fork-like construct found in each matching function is used.
func detectForks(root string, phases []spec.Phase) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)

	for pi, phase := range out {
		files, fset := parseGoFiles(root, phase.Files)
		if len(files) == 0 {
			continue
		}
		labelIdx := chipLabelIndexes(phase.Stages)
		// Build function line-span map for wide detection. Use all files from
		// sibling sub-phases (same package directory) so cross-sub-phase
		// branch target lookups resolve correctly after splitLargePhases.
		funcSpans := buildFuncSpans(root, phase, out)
		replacePhaseForks(fset, files, labelIdx, funcSpans, &out[pi])
	}

	return out
}

// buildFuncSpans parses every sibling file in the phase's package directory
// and maps top-level function name → body line span.
func buildFuncSpans(root string, phase spec.Phase, allPhases []spec.Phase) map[string]int {
	pkgFiles := collectPackageFiles(phase, allPhases)
	allFiles, allFset := parseGoFiles(root, pkgFiles)
	funcSpans := make(map[string]int)
	for _, f := range allFiles {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			start := allFset.Position(fn.Body.Pos()).Line
			end := allFset.Position(fn.Body.End()).Line
			funcSpans[fn.Name.Name] = end - start + 1
		}
	}
	return funcSpans
}

// replacePhaseForks swaps chip stages for fork stages when the chip's
// function contains a fork-like construct that survives the noise filters.
func replacePhaseForks(fset *token.FileSet, files []*ast.File, labelIdx map[string]int, funcSpans map[string]int, phase *spec.Phase) {
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			si, matched := labelIdx[fn.Name.Name]
			if !matched {
				continue
			}
			if replaceFuncFork(fset, fn, si, funcSpans, phase) {
				delete(labelIdx, fn.Name.Name) // only replace once per label
			}
		}
	}
}

// replaceFuncFork replaces the chip stage at index si with a fork stage when
// fn contains a meaningful fork construct. Reports whether it replaced.
func replaceFuncFork(fset *token.FileSet, fn *ast.FuncDecl, si int, funcSpans map[string]int, phase *spec.Phase) bool {
	fork := firstFork(fset, fn.Body)
	if fork == nil || !isMeaningfulFork(fork) {
		return false
	}
	phase.Stages[si] = spec.Stage{Fork: fork}
	markWideBranches(fork, funcSpans)
	return true
}

// markWideBranches flags fork branches whose target function spans more than
// 30 lines as wide.
func markWideBranches(fork *spec.Fork, funcSpans map[string]int) {
	for bi := range fork.Branches {
		if span, ok := funcSpans[fork.Branches[bi].Label]; ok && span > 30 {
			fork.Branches[bi].Wide = true
		}
	}
}

// isMeaningfulFork applies the fork noise filters: 2-branch if-else chains,
// forks with fewer than two meaningful branch labels, and homogeneous forks
// are all implementation detail rather than routing.
func isMeaningfulFork(fork *spec.Fork) bool {
	// Filter 1a: skip 2-branch if-else forks (implementation-detail
	// conditionals — one branch will have condition "else" or "…").
	if len(fork.Branches) == 2 && isIfElseFork(fork) {
		return false
	}
	if meaningfulForkBranchCount(fork) < 2 {
		return false
	}
	// Filter 1b: skip homogeneous forks where <30% of branch labels
	// are distinct (e.g., all branches call "WriteByte").
	return !isHomogeneousFork(fork)
}

// firstFork returns the first Fork found in body, or nil.
// Priority: switch → select → if/else-if chain.
func firstFork(fset *token.FileSet, body *ast.BlockStmt) *spec.Fork {
	var found *spec.Fork
	ast.Inspect(body, func(n ast.Node) bool {
		if found != nil {
			return false // already have one
		}
		if fork := constructFork(fset, n); fork != nil {
			found = fork
			return false
		}
		return true
	})
	return found
}

// constructFork converts one control-flow node into a Fork when it qualifies.
func constructFork(fset *token.FileSet, n ast.Node) *spec.Fork {
	switch s := n.(type) {
	case *ast.SwitchStmt:
		return switchFork(fset, s)
	case *ast.TypeSwitchStmt:
		return typeSwitchFork(fset, s)
	case *ast.SelectStmt:
		return selectFork(fset, s)
	case *ast.IfStmt:
		return ifFork(fset, s)
	}
	return nil
}
