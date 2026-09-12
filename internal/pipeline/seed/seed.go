// Package seed bundles starter specs and writes them to the user-level
// config directory on first run or when explicitly requested.
package seed

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

//go:embed embed/*.yaml
var embedded embed.FS

// Files returns the embedded starter spec filenames (e.g. "paper.yaml").
// Sorted for deterministic output.
func Files() []string {
	entries, err := embedded.ReadDir("embed")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Seed writes every embedded spec into dir. Creates dir if missing.
// If force is false and any target file already exists, returns an error
// naming the conflict before writing anything.
// Writes are atomic: tempfile in dir, then os.Rename.
func Seed(dir string, force bool) ([]string, error) {
	if dir == "" {
		return nil, errors.New("seed: empty dir")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("seed: mkdir %s: %w", dir, err)
	}
	names := Files()
	for _, n := range names {
		target := filepath.Join(dir, n)
		info, err := os.Lstat(target)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("seed: inspect %s: %w", target, err)
		}
		if err == nil && (!force || !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("seed: %s exists (regular files require --force to overwrite)", target)
		}
	}
	written := make([]string, 0, len(names))
	for _, n := range names {
		data, err := fs.ReadFile(embedded, "embed/"+n)
		if err != nil {
			return written, fmt.Errorf("seed: read embed %s: %w", n, err)
		}
		target := filepath.Join(dir, n)
		write := atomicfile.WriteNew
		if force {
			write = atomicfile.Write
		}
		if err := write(target, data, 0o600); err != nil {
			return written, err
		}
		written = append(written, target)
	}
	return written, nil
}

// IsEmptyDir returns true if dir does not exist OR contains no
// *.yaml / *.yml files.
func IsEmptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("seed: read dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext == ".yaml" || ext == ".yml" {
			return false, nil
		}
	}
	return true, nil
}
