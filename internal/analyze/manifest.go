package analyze

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dotcommander/pan/internal/config"
)

// Manifest returns SHA-256 hashes of all eligible regular files. It is the
// cache's content authority; source bytes are read only for this request.
// File contents are read through one root-scoped handle opened before the
// walk so no walked path can resolve outside the repository.
func Manifest(ctx context.Context, root string, cfg config.Config) (map[string]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	scope, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = scope.Close() }()
	visitor := manifestVisitor{ctx: ctx, scope: scope, root: root, excludes: config.NormalizeExcludes(cfg.Exclude), out: map[string]string{}}
	if err := filepath.WalkDir(root, visitor.visit); err != nil {
		return nil, err
	}
	return visitor.out, nil
}

// manifestVisitor hashes one walk subtree through a root-scoped handle.
type manifestVisitor struct {
	ctx      context.Context
	scope    *os.Root
	root     string
	excludes []string
	out      map[string]string
}

func (v manifestVisitor) visit(p string, d fs.DirEntry, walkErr error) error {
	if ctxErr := v.ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if walkErr != nil {
		return walkErr
	}
	rel, relErr := filepath.Rel(v.root, p)
	if relErr != nil {
		return relErr
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return nil
	}
	if d.IsDir() {
		return v.visitDir(rel)
	}
	return v.visitFile(rel, d)
}

func (v manifestVisitor) visitDir(rel string) error {
	if excluded(rel, v.excludes) {
		return filepath.SkipDir
	}
	return nil
}

func (v manifestVisitor) visitFile(rel string, d fs.DirEntry) error {
	if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
		return nil
	}
	data, err := v.scope.ReadFile(rel)
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	sum := sha256.Sum256(data)
	v.out[rel] = hex.EncodeToString(sum[:])
	return nil
}

// ManifestFromSnapshot hashes bytes captured during analysis.
func ManifestFromSnapshot(s Snapshot) map[string]string {
	out := make(map[string]string, len(s.Captured))
	for p, b := range s.Captured {
		sum := sha256.Sum256(b)
		out[p] = hex.EncodeToString(sum[:])
	}
	return out
}
