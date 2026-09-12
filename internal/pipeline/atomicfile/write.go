// Package atomicfile writes complete files without exposing partial output.
package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Write replaces path atomically. It preserves an existing file's mode and
// uses mode for a new file.
func Write(path string, data []byte, mode os.FileMode) error {
	return writeFile(path, data, mode, os.Rename)
}

// WriteNew publishes path only when it does not already exist. It avoids the
// check-then-replace race by linking the completed temporary file into place.
func WriteNew(path string, data []byte, mode os.FileMode) error {
	return writeFile(path, data, mode, func(tmp, dest string) error {
		if err := os.Link(tmp, dest); err != nil {
			return err
		}
		return os.Remove(tmp)
	})
}

func writeFile(path string, data []byte, mode os.FileMode, publish func(string, string) error) error {
	effectiveMode, err := outputMode(path, mode)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if mkdirErr := os.MkdirAll(dir, 0o750); mkdirErr != nil {
		return fmt.Errorf("create output directory: %w", mkdirErr)
	}
	tmp, err := os.CreateTemp(dir, ".pan-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary output: %w", err)
	}
	if err := tmp.Chmod(effectiveMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temporary output mode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary output: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := publish(tmpName, path); err != nil {
		return fmt.Errorf("publish output: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	return nil
}

func outputMode(path string, mode os.FileMode) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return 0, errors.New("output path is a directory")
		}
		return info.Mode().Perm(), nil
	}
	if !os.IsNotExist(err) {
		return 0, fmt.Errorf("stat output: %w", err)
	}
	return mode, nil
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open output directory: %w", err)
	}
	defer func() { _ = handle.Close() }()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync output directory: %w", err)
	}
	return nil
}
