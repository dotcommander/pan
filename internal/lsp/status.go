package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/config"
)

// defaultLookPath returns the command resolver used when a caller does not
// inject one.
func defaultLookPath() func(string) (string, error) {
	return exec.LookPath
}

// StatusReport describes language-server coverage for one root, detected
// from source file extensions and project root markers. Detection starts
// no servers, performs no queries, and never mutates anything.
type StatusReport struct {
	Root     string          `json:"root"`
	MaxDepth int             `json:"max_depth"`
	Servers  []StatusServer  `json:"servers"`
	Missing  []MissingServer `json:"missing"`
}

// StatusServer is one detected language with an available server command
// and the workspace roots it would serve.
type StatusServer struct {
	Language  string   `json:"language"`
	Root      string   `json:"root"`
	Command   string   `json:"command"`
	FileTypes []string `json:"file_types"`
}

// MissingServer is one detected language whose configured server commands
// are all unavailable on PATH; the extensions list the evidence found.
type MissingServer struct {
	Language        string   `json:"language"`
	TriedCommands   []string `json:"tried_commands"`
	FoundExtensions []string `json:"found_extensions"`
}

// DetectStatus scans root for source evidence and reports per-language
// server availability. The language table, walk depth, and server
// candidates come from rules; excludes are repository-relative paths to
// skip. lookPath resolves command availability (nil uses exec.LookPath).
func DetectStatus(ctx context.Context, root string, rules config.LspRules, excludes []string, lookPath func(string) (string, error)) (StatusReport, error) {
	if lookPath == nil {
		lookPath = defaultLookPath()
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return StatusReport{}, fmt.Errorf("lsp: resolve root: %w", err)
	}
	hints, err := collectHints(ctx, abs, rules, excludes)
	if err != nil {
		return StatusReport{}, err
	}
	report := StatusReport{
		Root:     abs,
		MaxDepth: rules.StatusMaxDepth,
		Servers:  []StatusServer{},
		Missing:  []MissingServer{},
	}
	for _, lang := range rules.Languages {
		found := foundExtensions(lang.FileTypes, hints.extensions)
		if len(found) == 0 {
			continue
		}
		roots := topmostRoots(hints.roots[lang.Language], abs)
		if command, ok := firstAvailable(lang.Servers, lookPath); ok {
			for _, root := range roots {
				report.Servers = append(report.Servers, StatusServer{
					Language:  lang.Language,
					Root:      root,
					Command:   command,
					FileTypes: slices.Clone(lang.FileTypes),
				})
			}
			continue
		}
		report.Missing = append(report.Missing, MissingServer{
			Language:        lang.Language,
			TriedCommands:   slices.Clone(lang.Servers),
			FoundExtensions: found,
		})
	}
	slices.SortFunc(report.Servers, func(a, b StatusServer) int {
		if c := strings.Compare(a.Root, b.Root); c != 0 {
			return c
		}
		return strings.Compare(a.Language, b.Language)
	})
	slices.SortFunc(report.Missing, func(a, b MissingServer) int {
		return strings.Compare(a.Language, b.Language)
	})
	return report, nil
}

// statusHints is the bounded evidence one status walk collected.
type statusHints struct {
	extensions map[string]bool
	roots      map[string][]string
}

// statusWalk carries one bounded status walk's state: the cancellation
// context, the walk root, the language rules, the excluded directories,
// and the collected evidence.
type statusWalk struct {
	ctx      context.Context
	root     string
	rules    config.LspRules
	excluded map[string]bool
	hints    statusHints
}

// collectHints walks root no deeper than the configured status depth,
// skipping excluded directories, and records found file extensions plus
// the directories that hold each language's root markers. Unreadable
// entries are skipped; a canceled context aborts the walk.
func collectHints(ctx context.Context, root string, rules config.LspRules, excludes []string) (statusHints, error) {
	excluded := make(map[string]bool, len(excludes))
	for _, entry := range excludes {
		excluded[entry] = true
	}
	walk := statusWalk{
		ctx:      ctx,
		root:     root,
		rules:    rules,
		excluded: excluded,
		hints: statusHints{
			extensions: make(map[string]bool, 16),
			roots:      make(map[string][]string, len(rules.Languages)),
		},
	}
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err == nil {
			return walk.visit(current, entry)
		}
		// A walk error is an unreadable entry: it is evidence of
		// nothing, so the walk continues instead of failing the report.
		return nil
	})
	if err != nil && ctx.Err() != nil {
		return statusHints{}, ctx.Err()
	}
	if err != nil {
		return statusHints{}, fmt.Errorf("lsp: walk %s: %w", root, err)
	}
	return walk.hints, nil
}

// visit records one readable walk entry: the root and marker directories
// are probed for language roots, excluded or too-deep directories are
// pruned, and file extensions are recorded. A canceled context aborts
// the walk.
func (w *statusWalk) visit(current string, entry os.DirEntry) error {
	if ctxErr := w.ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if current == w.root {
		w.recordMarkers(current)
		return nil
	}
	if entry.IsDir() {
		rel := relSlash(w.root, current)
		if w.excluded[rel] || entry.Name() == ".git" || depthOf(rel) > w.rules.StatusMaxDepth {
			return filepath.SkipDir
		}
		w.recordMarkers(current)
		return nil
	}
	if ext := strings.ToLower(filepath.Ext(current)); ext != "" {
		w.hints.extensions[ext] = true
	}
	return nil
}

// recordMarkers records dir as a candidate root for every language whose
// markers it directly contains. An unreadable directory contributes no
// marker evidence, so it is skipped rather than failing the walk.
func (w *statusWalk) recordMarkers(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	for _, lang := range w.rules.Languages {
		for _, marker := range lang.RootMarkers {
			if names[marker] {
				w.hints.roots[lang.Language] = append(w.hints.roots[lang.Language], dir)
				break
			}
		}
	}
}

// relSlash returns the root-relative slash path of current.
func relSlash(root, current string) string {
	rel, err := filepath.Rel(root, current)
	if err != nil {
		return filepath.Base(current)
	}
	return filepath.ToSlash(rel)
}

// depthOf returns the directory depth of one root-relative slash path.
func depthOf(rel string) int {
	if rel == "." {
		return 0
	}
	return len(strings.Split(rel, "/"))
}

// foundExtensions returns the configured file types actually seen.
func foundExtensions(fileTypes []string, found map[string]bool) []string {
	var out []string
	for _, fileType := range fileTypes {
		if found[fileType] {
			out = append(out, fileType)
		}
	}
	return out
}

// topmostRoots returns the shallowest marker roots, dropping roots nested
// beneath another root, and falls back to root itself when no marker was
// found. The result is sorted and deduplicated.
func topmostRoots(markerRoots []string, root string) []string {
	roots := slices.Clone(markerRoots)
	if len(roots) == 0 {
		return []string{root}
	}
	slices.Sort(roots)
	out := make([]string, 0, len(roots))
	for _, candidate := range roots {
		clean := filepath.Clean(candidate)
		if len(out) > 0 && isWithin(clean, out[len(out)-1]) {
			continue
		}
		out = append(out, clean)
	}
	return out
}

// isWithin reports whether target is parent or a descendant of it.
func isWithin(target, parent string) bool {
	rel, err := filepath.Rel(parent, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// firstAvailable returns the first command resolvable by lookPath.
func firstAvailable(commands []string, lookPath func(string) (string, error)) (string, bool) {
	for _, command := range commands {
		if _, err := lookPath(command); err == nil {
			return command, true
		}
	}
	return "", false
}
