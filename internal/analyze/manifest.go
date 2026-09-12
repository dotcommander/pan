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
func Manifest(ctx context.Context, root string, cfg config.Config) (map[string]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if excluded(rel, config.NormalizeExcludes(cfg.Exclude)) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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
