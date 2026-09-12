// Package initialize creates a starter pipeline spec for a repository.
package initialize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"gopkg.in/yaml.v3"
)

const specFilename = "pan.yaml"

// Result describes the files created or updated by Init.
type Result struct {
	Output  string
	Project string
}

// Init writes a starter spec and ensures out/ is ignored by Git.
func Init(root string, force bool) (Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve repository: %w", err)
	}
	output := filepath.Join(absRoot, specFilename)
	if linkErr := rejectSymlink(output); linkErr != nil {
		return Result{}, linkErr
	}
	if _, statErr := os.Lstat(output); statErr == nil && !force {
		return Result{}, fmt.Errorf("%s already exists (use --force to overwrite)", output)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Result{}, fmt.Errorf("inspect %s: %w", output, statErr)
	}
	project := spec.ProjectName(absRoot)
	content, err := starterSpec(project)
	if err != nil {
		return Result{}, err
	}
	if force {
		err = atomicfile.Write(output, content, 0o600)
	} else {
		err = atomicfile.WriteNew(output, content, 0o600)
	}
	if err != nil {
		return Result{}, fmt.Errorf("write %s: %w", output, err)
	}
	if err := ensureGitignoreEntry(absRoot, "out/"); err != nil {
		return Result{}, fmt.Errorf("update .gitignore: %w", err)
	}
	return Result{Output: output, Project: project}, nil
}

func starterSpec(project string) ([]byte, error) {
	title, err := yaml.Marshal(project)
	if err != nil {
		return nil, fmt.Errorf("marshal project title: %w", err)
	}
	return []byte("title: " + strings.TrimSpace(string(title)) + `
# breadcrumb: cmd/ → internal/... → main logic → output

phases:
  - name: Ingest
    kind: Fetch
    description: "load config and fetch raw data"
    stages:
      - { label: "init", style: boot }
      - { label: "fetch", subtitle: "HTTP / disk", style: io, wide: true }
      - { label: "normalize" }
    # sources:
    #   - "Source A"
    files:
      - internal/

  - name: Process
    kind: Transform
    description: "core processing logic"
    stages:
      - { label: "filter" }
      - fork:
          gate: "Mode?"
          branches:
            - { condition: "fast", label: "quick path" }
            - { condition: "full", label: "deep path", wide: true }
    files:
      - internal/

  - name: Emit
    kind: Emit
    description: "render and dispatch output"
    stages:
      - { label: "render", wide: true }
      - fanout:
          gate: "Dispatch"
          targets:
            - { flag: "--stdout", label: "stdout" }
            - { flag: "--file", label: "output file" }
    files:
      - internal/

# stores:
#   - name: cache/
#     writers:
#       - { stage: "fetch", access: "r/w" }
`), nil
}

func ensureGitignoreEntry(root, entry string) error {
	path := filepath.Join(root, ".gitignore")
	if err := rejectSymlink(path); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == entry || line == "/"+entry || line == strings.TrimSuffix(entry, "/") {
			return nil
		}
	}
	content := string(raw)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += entry + "\n"
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return atomicfile.WriteNew(path, []byte(content), 0o600)
	}
	return atomicfile.Write(path, []byte(content), 0o600)
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to overwrite symlink: %s", path)
	}
	return nil
}
