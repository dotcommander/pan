package scan

import (
	"go/ast"
	"go/token"
	"slices"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// ─── detectFanouts ────────────────────────────────────────────────────────────

// detectFanouts walks each phase's Go files looking for four patterns:
//  1. Cobra AddCommand clusters — 2+ AddCommand calls in the same function → Fanout inserted AFTER the matching chip.
//  2. Multiple goroutine launches — 2+ go stmts in the same function → Fanout inserted after the matching chip.
//  3. HTTP handler registrations — 2+ route registrations (Handle, Get, Post, etc.) → Fanout inserted after the matching chip.
//  4. Routing closures — a returned http.HandlerFunc dispatching to 2+ branch targets.
//
// A second pass appends an HTTP fanout to phases that produced none in the
// first pass.
func detectFanouts(root string, phases []spec.Phase) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)
	out = insertChipFanouts(root, out)
	out = appendFallbackHTTPFanout(root, out)
	return out
}

// fanoutInsertion is one (insert-after index, fanout) pair collected for a
// phase, spliced back-to-front after collection.
type fanoutInsertion struct {
	afterIdx int
	fanout   spec.Fanout
}

// insertChipFanouts runs the four fanout patterns against functions whose
// names match chip labels, splicing the resulting fanout stages after the
// matching chips.
func insertChipFanouts(root string, phases []spec.Phase) []spec.Phase {
	out := phases
	for pi, phase := range out {
		files, fset := parseGoFiles(root, phase.Files)
		if len(files) == 0 {
			continue
		}
		insertions := chipFanoutInsertions(files, fset, phase)
		if len(insertions) == 0 {
			continue
		}
		// Sort insertions highest-index first so splicing back-to-front
		// doesn't shift earlier indices.
		slices.SortFunc(insertions, func(a, b fanoutInsertion) int { return b.afterIdx - a.afterIdx })
		out[pi].Stages = spliceFanouts(out[pi].Stages, insertions)
	}
	return out
}

// chipFanoutInsertions collects the fanout insertions for one phase by
// matching top-level function declarations against its chip labels.
func chipFanoutInsertions(files []*ast.File, fset *token.FileSet, phase spec.Phase) []fanoutInsertion {
	labelIdx := chipLabelIndexes(phase.Stages)
	var insertions []fanoutInsertion
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
			if fo := matchingFanout(fset, fn.Body); fo != nil {
				insertions = append(insertions, fanoutInsertion{afterIdx: si, fanout: *fo})
			}
		}
	}
	return insertions
}

// matchingFanout returns the first fanout pattern matching the function
// body, in the documented priority order (AddCommand → goroutines → HTTP
// registrations → routing closure).
func matchingFanout(fset *token.FileSet, body *ast.BlockStmt) *spec.Fanout {
	if fo := addCommandFanout(fset, body); fo != nil {
		return fo
	}
	if fo := goroutineFanout(body); fo != nil {
		return fo
	}
	if fo := handlerFanout(fset, body); fo != nil {
		return fo
	}
	return routerClosureFanout(fset, body)
}

// chipLabelIndexes maps chip label → stage index for one phase.
func chipLabelIndexes(stages []spec.Stage) map[string]int {
	labelIdx := make(map[string]int)
	for si, s := range stages {
		if s.Chip != nil {
			labelIdx[s.Chip.Label] = si
		}
	}
	return labelIdx
}

// spliceFanouts inserts one fanout stage after each insertion point, applied
// from the back to the front so earlier indices stay valid.
func spliceFanouts(stages []spec.Stage, insertions []fanoutInsertion) []spec.Stage {
	for _, ins := range insertions {
		newStage := spec.Stage{Fanout: &spec.Fanout{Gate: ins.fanout.Gate, Targets: ins.fanout.Targets}}
		stages = append(stages[:ins.afterIdx+1],
			append([]spec.Stage{newStage}, stages[ins.afterIdx+1:]...)...)
	}
	return stages
}

// appendFallbackHTTPFanout scans for HTTP handler registrations in any
// function, even if it did not match a chip label. Only phases that did not
// already get an HTTP fanout from the chip pass receive one.
func appendFallbackHTTPFanout(root string, phases []spec.Phase) []spec.Phase {
	out := phases
	for pi, phase := range out {
		if phaseHasHTTPFanout(phase) {
			continue
		}
		if fo := firstHTTPFanout(root, phase.Files); fo != nil {
			out[pi].Stages = append(out[pi].Stages, spec.Stage{Fanout: fo})
		}
	}
	return out
}

// phaseHasHTTPFanout reports whether any stage already carries an HTTP-routes
// fanout.
func phaseHasHTTPFanout(phase spec.Phase) bool {
	for _, s := range phase.Stages {
		if s.Fanout != nil && s.Fanout.Gate == gateHTTPRoutes {
			return true
		}
	}
	return false
}

// firstHTTPFanout returns the first HTTP-routes fanout found in any function
// of the given files, preferring plain registrations over routing closures.
func firstHTTPFanout(root string, files []string) *spec.Fanout {
	parsed, fset := parseGoFiles(root, files)
	for _, f := range parsed {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fo := handlerFanout(fset, fn.Body); fo != nil {
				return fanoutCopy(fo)
			}
			if fo := routerClosureFanout(fset, fn.Body); fo != nil {
				return fanoutCopy(fo)
			}
		}
	}
	return nil
}

// fanoutCopy returns a heap-allocated copy of a fanout value.
func fanoutCopy(fo *spec.Fanout) *spec.Fanout {
	return &spec.Fanout{Gate: fo.Gate, Targets: fo.Targets}
}

// cmdMode records one dispatch target and its detected output mode
// ("file" or "HTTP").
type cmdMode struct {
	name string
	mode string
}

// detectEmitFork scans for cobra commands with distinct output modes (file write vs HTTP)
// and inserts a Fork in the Emit phase when exactly two such commands exist.
func detectEmitFork(root string, phases []spec.Phase) []spec.Phase {
	out := make([]spec.Phase, len(phases))
	copy(out, phases)

	// Find the Emit phase.
	emitIdx := -1
	for i, p := range out {
		if p.Kind == "Emit" {
			emitIdx = i
			break
		}
	}
	if emitIdx < 0 {
		return out
	}

	modes := commandOutputModes(root, phases)
	if !hasFileAndHTTPModes(modes) {
		return out
	}

	// Insert as first stage of Emit phase.
	fork := &spec.Fork{Gate: commandWord, Branches: emitForkBranches(dedupeModes(modes))}
	out[emitIdx].Stages = append([]spec.Stage{{Fork: fork}}, out[emitIdx].Stages...)
	return out
}

// commandOutputModes classifies the output mode of every target of every
// subcommand fanout across the phases.
func commandOutputModes(root string, phases []spec.Phase) []cmdMode {
	var modes []cmdMode
	for _, p := range phases {
		for _, s := range p.Stages {
			if s.Fanout == nil || s.Fanout.Gate != gateSubcommand {
				continue
			}
			for _, t := range s.Fanout.Targets {
				if mode := detectOutputMode(root, phases, t.Label); mode != "" {
					modes = append(modes, cmdMode{name: t.Flag, mode: mode})
				}
			}
		}
	}
	return modes
}

// hasFileAndHTTPModes reports whether at least one command writes files and
// at least one serves HTTP.
func hasFileAndHTTPModes(modes []cmdMode) bool {
	fileMode, httpMode := false, false
	for _, m := range modes {
		if m.mode == outputModeFile {
			fileMode = true
		}
		if m.mode == outputModeHTTP {
			httpMode = true
		}
	}
	return fileMode && httpMode
}

// dedupeModes keeps only the first command for each distinct mode.
func dedupeModes(modes []cmdMode) []cmdMode {
	seen := make(map[string]bool)
	var deduped []cmdMode
	for _, m := range modes {
		if !seen[m.mode] {
			seen[m.mode] = true
			deduped = append(deduped, m)
		}
	}
	return deduped
}

// emitForkBranches builds one fork branch per command mode.
func emitForkBranches(modes []cmdMode) []spec.Branch {
	var branches []spec.Branch
	for _, m := range modes {
		label := "write file"
		if m.mode == outputModeHTTP {
			label = "HTTP server"
		}
		branches = append(branches, spec.Branch{
			Condition: m.name,
			Label:     label,
		})
	}
	return branches
}
