package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeContextArtifact atomically replaces a regular output file. It refuses
// symlinks so an explicit CLI artifact cannot be redirected unexpectedly.
func writeContextArtifact(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("artifact path must be a regular file: %s", path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect artifact path: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set artifact mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write artifact: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close artifact: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace artifact: %w", err)
	}
	return nil
}
