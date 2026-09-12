// Package repo discovers repository-level instruction assets (AGENTS.md,
// CLAUDE.md) within an explicitly bounded scope.
package repo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/config"
)

// defaultMaxInstructions bounds instruction discovery when Scope.MaxFiles is
// unset, so the walk can never run away on adversarial trees.
const defaultMaxInstructions = 1000

// Scope bounds instruction discovery. Exclude holds repo-relative directory
// paths with the same semantics as config.Config.Exclude; MaxFiles <= 0 means
// the package default.
type Scope struct {
	Exclude  []string
	MaxFiles int
}

// Instructions returns the sorted, repo-relative slash paths of instruction
// files under root. Excluded directories are scoped out entirely, symlinked
// files are not trusted, and discovery stops at the scope bound.
// truncated reports whether that bound stopped the walk before every
// instruction file was seen.
func Instructions(root string, scope Scope) (paths []string, truncated bool, err error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, false, fmt.Errorf("repository root must be a directory: %s", root)
	}
	maxFiles := scope.MaxFiles
	if maxFiles <= 0 {
		maxFiles = defaultMaxInstructions
	}
	walk := instructionWalk{
		root:    root,
		exclude: config.NormalizeExcludes(scope.Exclude),
		max:     maxFiles,
	}
	walkErr := filepath.WalkDir(root, walk.visit)
	if walkErr != nil {
		return nil, false, fmt.Errorf("discover instructions: %w", walkErr)
	}
	slices.Sort(walk.paths)
	return walk.paths, walk.truncated, nil
}

// instructionWalk accumulates instruction-file paths for one bounded walk.
type instructionWalk struct {
	root      string
	exclude   []string
	max       int
	paths     []string
	truncated bool
}

// visit is the filepath.WalkDir callback: excluded directories scope the
// walk out, symlinked and irregular files are not trusted, and discovery
// stops at the scope bound.
func (w *instructionWalk) visit(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		if path != w.root && errors.Is(walkErr, fs.ErrPermission) {
			return nil
		}
		return walkErr
	}
	rel, relErr := filepath.Rel(w.root, path)
	if relErr != nil {
		return relErr
	}
	if rel == "." {
		return nil
	}
	slashRel := filepath.ToSlash(rel)
	if d.IsDir() {
		if excluded(slashRel, w.exclude) {
			return filepath.SkipDir
		}
		return nil
	}
	if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
		return nil
	}
	base := d.Name()
	if base != "AGENTS.md" && base != "CLAUDE.md" {
		return nil
	}
	if len(w.paths) >= w.max {
		w.truncated = true
		return fs.SkipAll
	}
	w.paths = append(w.paths, slashRel)
	return nil
}

func excluded(path string, names []string) bool {
	for _, name := range names {
		if path == name || strings.HasPrefix(path, name+"/") {
			return true
		}
	}
	return false
}
