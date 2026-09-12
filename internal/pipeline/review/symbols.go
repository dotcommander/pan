package review

import (
	"path/filepath"
	"regexp"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// exportedGoIdent matches an exported Go identifier: starts with uppercase.
var exportedGoIdent = regexp.MustCompile(`^[A-Z][a-zA-Z0-9_]*$`)

// buildSymbolIndex builds a map of symbol name → list of absolute file paths
// from the Pan ranked files.
func buildSymbolIndex(root string, ranked []symbols.RankedFile) map[string][]string {
	idx := make(map[string][]string)
	for _, rf := range ranked {
		if rf.FileSymbols == nil {
			continue
		}
		for _, sym := range rf.Symbols {
			if sym.Exported {
				idx[sym.Name] = append(idx[sym.Name], rootedPath(root, rf.Path))
			}
		}
	}
	return idx
}

// extractStageLabels returns all human-visible labels from a stage.
func extractStageLabels(st spec.Stage) []string {
	if st.Chip != nil {
		return []string{st.Chip.Label}
	}
	return nil
}

// checkSymbolCoverage verifies that chip/fork/fanout labels matching exported
// Go identifiers can be found in the Pan symbol index.
func checkSymbolCoverage(s *spec.Spec, root string, symIdx map[string][]string) []Finding {
	var findings []Finding
	for _, p := range s.AllPhases() {
		phaseFiles := phaseFileSet(root, p)
		for _, st := range p.Stages {
			findings = append(findings, stageCoverageFindings(p, root, st, phaseFiles, symIdx)...)
		}
	}
	return findings
}

// phaseFileSet expands one phase's file declarations, globs included, into a
// set of absolute paths. Unmatched globs contribute nothing.
func phaseFileSet(root string, p spec.Phase) map[string]bool {
	phaseFiles := make(map[string]bool, len(p.Files))
	for _, f := range p.Files {
		abs := f
		if !filepath.IsAbs(f) {
			abs = filepath.Join(root, f)
		}
		if spec.HasGlobMeta(f) {
			if matches, err := filepath.Glob(abs); err == nil {
				for _, m := range matches {
					phaseFiles[m] = true
				}
			}
			continue
		}
		phaseFiles[filepath.Clean(abs)] = true
	}
	return phaseFiles
}

// stageCoverageFindings cross-checks one stage's labels against the symbol
// index and the phase's declared files.
func stageCoverageFindings(p spec.Phase, root string, st spec.Stage, phaseFiles map[string]bool, symIdx map[string][]string) []Finding {
	var findings []Finding
	for _, label := range extractStageLabels(st) {
		if !exportedGoIdent.MatchString(label) {
			continue
		}
		paths, ok := symIdx[label]
		if !ok {
			findings = append(findings, Finding{
				Check:    checkNameSymbolCoverage,
				Severity: High,
				Phase:    p.Name,
				Message:  "symbol " + label + " not found in codebase",
			})
			continue
		}
		// Symbol exists — check whether it lives in the phase's files.
		if len(phaseFiles) == 0 {
			continue // no file declarations to cross-check
		}
		if symbolInPhase(paths, phaseFiles) {
			continue
		}
		// Report the first file where the symbol actually lives.
		rel, _ := filepath.Rel(root, paths[0])
		if rel == "" {
			rel = paths[0]
		}
		findings = append(findings, Finding{
			Check:    checkNameSymbolCoverage,
			Severity: Medium,
			Phase:    p.Name,
			Message:  "symbol " + label + " found in " + rel + " but spec places it in phase " + p.Name,
		})
	}
	return findings
}

// symbolInPhase reports whether any of the symbol's files is declared in the
// phase's file set.
func symbolInPhase(paths []string, phaseFiles map[string]bool) bool {
	for _, path := range paths {
		if phaseFiles[path] {
			return true
		}
	}
	return false
}

// checkStoreFreshness verifies that store writer stages that look like Go
// identifiers exist in the symbol index. Also checks store names that look
// like file paths.
func checkStoreFreshness(s *spec.Spec, symIdx map[string][]string) []Finding {
	var findings []Finding
	knownLabels := stageLabelSet(s)
	for _, store := range s.Stores {
		findings = append(findings, storeWriterFindings(store, knownLabels, symIdx)...)
	}
	return findings
}

// stageLabelSet collects every phase name and stage label declared in the
// spec into one lookup set.
func stageLabelSet(s *spec.Spec) map[string]bool {
	knownLabels := make(map[string]bool)
	for _, phase := range s.AllPhases() {
		knownLabels[phase.Name] = true
		for _, stage := range phase.Stages {
			for _, label := range extractStageLabels(stage) {
				knownLabels[label] = true
			}
		}
	}
	return knownLabels
}

// storeWriterFindings reports store writers whose stage name looks like a Go
// identifier but is neither a known stage label nor an indexed symbol.
func storeWriterFindings(store spec.Store, knownLabels map[string]bool, symIdx map[string][]string) []Finding {
	var findings []Finding
	for _, w := range store.Writers {
		if w.Stage == "" || knownLabels[w.Stage] {
			continue
		}
		if !exportedGoIdent.MatchString(w.Stage) {
			continue
		}
		if _, ok := symIdx[w.Stage]; !ok {
			findings = append(findings, Finding{
				Check:    checkNameStoreFreshness,
				Severity: Medium,
				Message:  "store writer stage " + w.Stage + " not found as symbol (store: " + store.Name + ")",
			})
		}
	}
	return findings
}
