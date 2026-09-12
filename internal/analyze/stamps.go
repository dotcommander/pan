package analyze

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/dotcommander/pan/internal/config"
)

// FileStamp is a file-identity fingerprint: size plus modification time. It
// is the freshness unit for the analysis cache; stamps intentionally do not
// hash content, so a same-size same-mtime rewrite is indistinguishable and
// reported as fresh.
type FileStamp struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// Stamps walks root under cfg's exclusion policy and returns the identity
// stamp of every regular, non-symlinked file, keyed by repo-relative slash
// path. Unlike Build, stamping applies no size or count bounds: the stamp set
// describes the whole tree so cache freshness detects additions and removals
// that a bounded analysis pass would never observe.
func Stamps(ctx context.Context, root string, cfg config.Config) (map[string]FileStamp, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("repository root must be a directory: %s", root)
	}
	exclude := config.NormalizeExcludes(cfg.Exclude)
	stamps := make(map[string]FileStamp)
	walker := &stampWalker{root: root, exclude: exclude, stamps: stamps}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		return walker.visit(ctx, path, d, walkErr)
	})
	if err != nil {
		return nil, fmt.Errorf("stamp repository: %w", err)
	}
	return stamps, nil
}

// stampWalker accumulates identity stamps for one bounded walk.
type stampWalker struct {
	root    string
	exclude []string
	stamps  map[string]FileStamp
}

// visit is the filepath.WalkDir callback: it records the identity stamp of
// one walked entry. Excluded directories scope the walk out, and symlinks
// and irregular files are skipped without failing the walk.
func (w *stampWalker) visit(ctx context.Context, path string, d fs.DirEntry, walkErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return walkErr
	}
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return err
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
	info, ok := entryInfo(d)
	if ok {
		w.stamps[slashRel] = FileStamp{Size: info.Size(), ModTime: info.ModTime()}
	}
	return nil // stat races are freshness noise, not walk failures
}

// entryInfo returns the entry's FileInfo, or ok false when the stat races
// with a concurrent removal. Callers treat a failed stat as a skip, never
// as a walk failure.
func entryInfo(d fs.DirEntry) (fs.FileInfo, bool) {
	info, err := d.Info()
	if err != nil {
		return nil, false
	}
	return info, true
}
