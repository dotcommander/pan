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
	return writeFile(path, data, mode, replaceFile)
}

// WriteNew publishes path only when it does not already exist. It avoids the
// check-then-replace race by linking the completed temporary file into place.
func WriteNew(path string, data []byte, mode os.FileMode) error {
	return writeFile(path, data, mode, publishNew)
}

func writeFile(path string, data []byte, mode os.FileMode, publish func(string, string) error) error {
	return streamFile(path, mode, publish, func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	})
}

// StreamNew streams a complete file into private staging and publishes exclusively.
func StreamNew(path string, mode os.FileMode, write func(io.Writer) error) error {
	return streamFile(path, mode, publishNew, write)
}

// PublishedError means the complete destination exists, but durability failed.
// Callers must retain it and must not treat it as an unpublished temporary file.
type PublishedError struct{ Err error }

func (e *PublishedError) Error() string { return "output published: " + e.Err.Error() }
func (e *PublishedError) Unwrap() error { return e.Err }

type stagedFile interface {
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}
type fileOps struct {
	create  func(string, string) (stagedFile, error)
	syncDir func(string) error
}

func streamFile(path string, mode os.FileMode, publish func(string, string) error, write func(io.Writer) error) error {
	return streamFileWith(path, mode, publish, write, fileOps{
		create:  func(dir, pattern string) (stagedFile, error) { return os.CreateTemp(dir, pattern) },
		syncDir: syncDir,
	})
}

func streamFileWith(path string, mode os.FileMode, publish func(string, string) error, write func(io.Writer) error, ops fileOps) error {
	effectiveMode, err := outputMode(path, mode)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if mkdirErr := os.MkdirAll(dir, 0o750); mkdirErr != nil {
		return fmt.Errorf("create output directory: %w", mkdirErr)
	}
	tmp, err := ops.create(dir, ".pan-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := write(tmp); err != nil {
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
	if err := ops.syncDir(dir); err != nil {
		return &PublishedError{Err: err}
	}
	return nil
}

func outputMode(path string, mode os.FileMode) (os.FileMode, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return 0, errors.New("output path is not a regular file")
		}
		return info.Mode().Perm(), nil
	}
	if !os.IsNotExist(err) {
		return 0, fmt.Errorf("stat output: %w", err)
	}
	return mode, nil
}
