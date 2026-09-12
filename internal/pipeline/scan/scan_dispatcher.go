package scan

import (
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// buildDispatcherSpec produces a Spec with:
//   - Spec.Phases = shared infrastructure and model phases used across commands
//   - Spec.Commands[i].Phases = per-command phases (command-specific closure)
func buildDispatcherSpec(absRoot, title string, ranked []symbols.RankedFile, cfg Config, info dispatchInfo) (*spec.Spec, error) {
	// 1. Build ranked lookup by relative path for fast sub-setting.
	rankedByPath := make(map[string]symbols.RankedFile, len(ranked))
	for _, rf := range ranked {
		rankedByPath[relPath(absRoot, rf.Path)] = rf
	}

	// 2. Collect the shared (prelude) files. These are files in info.sharedFiles
	//    plus cmd/ and internal/commands/ files (Boot + Dispatch entry points).
	preludeFiles := collectPreludeFiles(absRoot, info, ranked)

	// 3. Build prelude phases using the flat groupPhases pipeline, scoped to prelude files.
	preludeRanked := filterRanked(ranked, preludeFiles, absRoot)
	preludePhases := runPreludeChain(absRoot, preludeRanked, cfg)
	renameRootPhases(preludePhases, filepath.Base(absRoot), "Runtime")

	// 4. Build per-command phases (sorted alphabetically for determinism).
	cmdNames := sortedCommandNames(info.commandFiles)
	commands := make([]spec.Command, 0, len(cmdNames))
	for _, name := range cmdNames {
		closure := info.commandClosure[name]
		// Exclude shared files from command closures (they live in the prelude).
		commandFiles := make(map[string]bool, len(closure))
		for f := range closure {
			if !info.sharedFiles[f] && !preludeFiles[f] {
				commandFiles[f] = true
			}
		}
		// Keep a shared dispatcher in the prelude. stdlib-flag root CLIs use the
		// same main.go as every command entry; putting it back here would make it
		// appear as a misleading command-specific runtime phase.
		if own := info.commandFiles[name]; own != "" &&
			(!info.sharedFiles[own] && !preludeFiles[own]) {
			commandFiles[own] = true
		}
		if len(commandFiles) == 0 {
			// Command with no private files — add a minimal placeholder phase. A
			// stdlib command can legitimately share all implementation with the
			// dispatcher, which already appears in the prelude.
			files := []string(nil)
			if info.commandLocalFiles == nil {
				files = []string{info.commandFiles[name]}
			}
			commands = append(commands, spec.Command{
				Name: name,
				Phases: []spec.Phase{{
					Name:  "Run",
					Kind:  "Emit",
					Files: files,
				}},
			})
			continue
		}

		cmdRanked := filterRanked(ranked, commandFiles, absRoot)
		cmdCfg := cfg
		if cmdCfg.MaxPhases > 5 {
			cmdCfg.MaxPhases = 5 // keep per-command lanes tighter
		}
		// runCommandChain preserves the prior groupPhasesPerCommand prefix +
		// command tail (forks/fanouts/subtitles/sources) in one owned chain.
		cmdPhases := runCommandChain(absRoot, cmdRanked, cmdCfg)
		renameRootPhases(cmdPhases, filepath.Base(absRoot), capitalize(name))

		commands = append(commands, spec.Command{
			Name:   name,
			Phases: cmdPhases,
		})
	}

	// 5. Detect stores across ALL phases (prelude + all command phases).
	// Build a temporary spec so we can use the canonical AllPhases helper.
	allPhases := (&spec.Spec{Phases: preludePhases, Commands: commands}).AllPhases()
	stores, allPhases := detectStores(absRoot, allPhases)
	phaseIndex := 0
	preludePhases = append([]spec.Phase(nil), allPhases[:len(preludePhases)]...)
	phaseIndex += len(preludePhases)
	for i := range commands {
		count := len(commands[i].Phases)
		commands[i].Phases = append([]spec.Phase(nil), allPhases[phaseIndex:phaseIndex+count]...)
		phaseIndex += count
	}

	// 6. Coverage across prelude + all command phases.
	coverage := computeCoverage(ranked, allPhases, absRoot, scanMaxFileSize)

	breadcrumb := buildBreadcrumb(absRoot, preludePhases)

	return &spec.Spec{
		Title:      title,
		Breadcrumb: breadcrumb,
		Phases:     preludePhases,
		Commands:   commands,
		Stores:     stores,
		Coverage:   coverage,
	}, nil
}

func renameRootPhases(phases []spec.Phase, rootName, replacement string) {
	for index := range phases {
		if strings.EqualFold(phases[index].Name, rootName) {
			phases[index].Name = replacement
		}
	}
}

// collectPreludeFiles builds the set of files that belong in the Boot/Dispatch
// prelude: shared infrastructure + cmd/ files + internal/commands/ files.
func collectPreludeFiles(absRoot string, info dispatchInfo, ranked []symbols.RankedFile) map[string]bool {
	prelude := make(map[string]bool)
	// Shared files (reachable from majority of commands).
	for f := range info.sharedFiles {
		prelude[f] = true
	}
	// cmd/ and internal/commands/ files are always in the prelude.
	for _, rf := range ranked {
		if rf.FileSymbols == nil {
			continue
		}
		rel := relPath(absRoot, rf.Path)
		dir := filepath.ToSlash(filepath.Dir(rel))
		if strings.HasPrefix(dir, "cmd/") ||
			dir == "internal/commands" || dir == "internal/command" {
			prelude[rel] = true
		}
	}
	return prelude
}

// filterRanked returns the subset of ranked files whose relative paths are in fileSet.
func filterRanked(ranked []symbols.RankedFile, fileSet map[string]bool, absRoot string) []symbols.RankedFile {
	var out []symbols.RankedFile
	for _, rf := range ranked {
		if rf.FileSymbols == nil {
			continue
		}
		rel := relPath(absRoot, rf.Path)
		if fileSet[rel] {
			out = append(out, rf)
		}
	}
	return out
}
