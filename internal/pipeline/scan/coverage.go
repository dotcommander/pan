package scan

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// countableGoSource reports whether rel is a Go source file that counts toward
// coverage: a .go file that is neither a test file nor a utility file.
func countableGoSource(rel string) bool {
	return strings.HasSuffix(rel, ".go") &&
		!strings.HasSuffix(rel, "_test.go")
}

// oversizedGoFiles returns repo-relative non-test .go source files that exceed
// maxFileSize and are absent from ranked — i.e. files Pan dropped at its
// size gate. computeCoverage folds these into the total + missing sets so a
// size-skipped source file is never silently counted as fully covered.
func oversizedGoFiles(root string, maxFileSize int, ranked []symbols.RankedFile) []string {
	inRanked := make(map[string]bool, len(ranked))
	for _, rf := range ranked {
		inRanked[relPath(root, rf.Path)] = true
	}
	collector := &oversizedCollector{root: root, maxFileSize: int64(maxFileSize), inRanked: inRanked}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil {
			return collector.visit(path, d)
		}
		return nil
	})
	return collector.skipped
}

// oversizedCollector records oversized non-test Go source files that ranking
// dropped, pruning directories that are never analyzed from the walk.
type oversizedCollector struct {
	root        string
	maxFileSize int64
	inRanked    map[string]bool
	skipped     []string
}

// visit records one walk entry when it is an oversized non-test Go source
// file that ranking dropped; directories that are never analyzed prune the
// walk.
func (c *oversizedCollector) visit(path string, d fs.DirEntry) error {
	if d.IsDir() {
		return c.visitDir(path, d)
	}
	c.visitFile(path, d)
	return nil
}

// visitDir prunes vendor, testdata, and dot directories from the walk.
func (c *oversizedCollector) visitDir(path string, d fs.DirEntry) error {
	name := d.Name()
	if path != c.root && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".")) {
		return filepath.SkipDir
	}
	return nil
}

// visitFile records one file when it is countable Go source, unranked, and
// larger than the size cap.
func (c *oversizedCollector) visitFile(path string, d fs.DirEntry) {
	rel := relPath(c.root, path)
	if !countableGoSource(rel) || c.inRanked[rel] {
		return
	}
	if info, err := d.Info(); err == nil && info.Size() > c.maxFileSize {
		c.skipped = append(c.skipped, rel)
	}
}

// computeCoverage counts how many Go source files (non-test) are represented
// across all phase.Files entries versus the total Pan-walked set.
// Uses the ranked list as the single authoritative source — no second filesystem walk.
func computeCoverage(ranked []symbols.RankedFile, phases []spec.Phase, root string, maxFileSize int) *spec.Coverage {
	total := coverageTotalSet(ranked, root, maxFileSize)
	represented := coverageRepresentedSet(phases, root, total)

	// Missing = total − represented, sorted for determinism.
	missing := make([]string, 0, len(total)-len(represented))
	for rel := range total {
		if !represented[rel] {
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)

	return &spec.Coverage{
		Represented: len(represented),
		Total:       len(total),
		Missing:     missing,
	}
}

// coverageTotalSet builds the denominator: every countable Go source file in
// the ranked walk, plus source files ranking dropped for exceeding the size
// cap so they surface as missing instead of vanishing.
func coverageTotalSet(ranked []symbols.RankedFile, root string, maxFileSize int) map[string]bool {
	total := make(map[string]bool, len(ranked))
	for _, rf := range ranked {
		rel := relPath(root, rf.Path)
		if !countableGoSource(rel) {
			continue
		}
		total[rel] = true
	}
	for _, rel := range oversizedGoFiles(root, maxFileSize, ranked) {
		total[rel] = true
	}
	return total
}

// coverageRepresentedSet builds the numerator: the union of all phase.Files
// entries, resolving globs, restricted to files in the total set.
func coverageRepresentedSet(phases []spec.Phase, root string, total map[string]bool) map[string]bool {
	represented := make(map[string]bool)
	for _, phase := range phases {
		for _, entry := range phase.Files {
			markRepresented(represented, total, root, entry)
		}
	}
	return represented
}

// markRepresented marks one phase.Files entry into the represented set: a
// literal path when it belongs to the total set, or every total-set match of
// a glob pattern.
func markRepresented(represented, total map[string]bool, root, entry string) {
	if strings.ContainsAny(entry, "*?[") {
		markGlobMatches(represented, total, root, entry)
		return
	}
	if total[entry] {
		represented[entry] = true
	}
}

// markGlobMatches expands one glob pattern relative to root and marks the
// matches that belong to the total set.
func markGlobMatches(represented, total map[string]bool, root, entry string) {
	matches, err := filepath.Glob(filepath.Join(root, entry))
	if err != nil {
		return
	}
	for _, abs := range matches {
		if rel := relPath(root, abs); total[rel] {
			represented[rel] = true
		}
	}
}
