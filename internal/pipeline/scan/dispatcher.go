package scan

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// dispatchInfo holds the detected per-command metadata from detectDispatchShape.
type dispatchInfo struct {
	// commandFiles maps command name → its dispatch entry file.
	commandFiles map[string]string
	// commandLocalFiles maps command name → same-package files reached from
	// that command's dispatcher branch. It is populated for root package main
	// stdlib-flag CLIs, where imports alone cannot distinguish handlers.
	commandLocalFiles map[string]map[string]bool
	// commandClosure maps command name → the full set of files transitively reachable
	// from that command's entry file via the import graph.
	commandClosure map[string]map[string]bool
	// sharedFiles is the set of files reachable from more than half the commands
	// (majority share → prelude / Boot+Dispatch phases).
	sharedFiles map[string]bool
	// modulePath is the Go module path read from go.mod.
	modulePath string
}

// newCmdPattern matches Cobra command constructors regardless of parameter list.
// Matches both unexported newFooCmd and exported NewFooCmd forms.
var newCmdPattern = regexp.MustCompile(`func (?:new|New)\w+Cmd\(`)

// cobraFallbackCommandFiles falls back from the stdlib-flag shape to the
// Cobra shapes: first internal/commands with newXCmd constructors, then a
// flat cmd/ package with cobra.Command literals. It returns nil when neither
// shape is present.
func cobraFallbackCommandFiles(root, modulePath string) map[string]string {
	cmdDir := filepath.Join(root, "cmd")
	if _, err := os.Stat(cmdDir); err != nil {
		return nil
	}
	commandsDir := filepath.Join(root, "internal", "commands")
	commandFiles, err := findCommandFiles(commandsDir)
	if err == nil && len(commandFiles) >= 3 {
		return commandFiles
	}
	if cmdCommands := findCobraCommandsInCmd(root, modulePath); len(cmdCommands) >= 3 {
		return cmdCommands
	}
	return nil
}

// detectDispatchShape inspects root for a dispatcher-style CLI:
//   - cmd/<name>/main.go exists
//   - ≥3 internal/commands/*.go files expose newXCmd() constructors
//
// Returns (info, true) when the pattern is detected, (empty, false) otherwise.
// If go.mod is absent, returns (empty, false) — fall through to flat shape.
func detectDispatchShape(root string, ranked []symbols.RankedFile) (dispatchInfo, bool) {
	// 1. Require go.mod to resolve module-relative import paths.
	modulePath, ok := readModulePath(root)
	if !ok {
		return dispatchInfo{}, false
	}

	// 2. Prefer the root package main stdlib-flag shape. It deliberately only
	// considers the repository root, so nested package-main tools (such as
	// release helpers) cannot become sequential application phases.
	commandFiles, commandLocalFiles := findStdlibFlagCommandFiles(root)

	// 3. Preserve the existing Cobra shapes as fallbacks.
	if len(commandFiles) < 3 {
		commandFiles = cobraFallbackCommandFiles(root, modulePath)
	}
	if len(commandFiles) < 3 {
		return dispatchInfo{}, false
	}

	// 4. Build import graph: importPath → []file-relative-paths.
	// RankedFile.FileSymbols.ImportPath is the Go import path; Imports are import paths.
	importGraph := buildImportGraph(ranked, root)

	// 5. For each command, union the import closures of its dispatcher entry
	// and its same-package handler files. A package import graph has no edges
	// between files in one package, so the AST-derived local files seed this walk.
	commandClosure := make(map[string]map[string]bool, len(commandFiles))
	for cmdName, relPath := range commandFiles {
		seeds := map[string]bool{relPath: true}
		for local := range commandLocalFiles[cmdName] {
			seeds[local] = true
		}
		closure := make(map[string]bool)
		for seed := range seeds {
			for file := range transitiveImportClosure(seed, importGraph) {
				closure[file] = true
			}
		}
		commandClosure[cmdName] = closure
	}

	// 6. Identify shared files: reachable from >len(commandFiles)/2 commands.
	threshold := len(commandFiles) / 2
	fileCount := make(map[string]int)
	for _, closure := range commandClosure {
		for f := range closure {
			fileCount[f]++
		}
	}
	sharedFiles := make(map[string]bool)
	for f, count := range fileCount {
		if count > threshold {
			sharedFiles[f] = true
		}
	}

	return dispatchInfo{
		commandFiles:      commandFiles,
		commandLocalFiles: commandLocalFiles,
		commandClosure:    commandClosure,
		sharedFiles:       sharedFiles,
		modulePath:        modulePath,
	}, true
}
