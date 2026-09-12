package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// FlowScanCmd is `pan flow scan`.
type FlowScanCmd struct {
	Path      string `arg:"" optional:"" help:"Repository path to scan (default: --repo)."`
	Output    string `name:"output" short:"o" help:"Output path, or - for raw YAML on stdout."`
	MaxPhases int    `name:"max-phases" default:"9" help:"Maximum phases; must be non-negative."`
	MaxStages int    `name:"max-stages" default:"7" help:"Maximum stages per phase; must be non-negative."`
	Update    string `name:"update" help:"Merge into an existing pipeline spec."`
	Force     bool   `name:"force" help:"Overwrite an existing output file."`
}

// Validate rejects scanner bounds before repository work starts.
func (c FlowScanCmd) Validate() error {
	return scan.ValidateConfig(scan.Config{MaxPhases: c.MaxPhases, MaxStages: c.MaxStages})
}

// Run executes `pan flow scan`.
func (c FlowScanCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	if c.Output == "-" && root.Format == formatJSON {
		return errors.New("--format json is incompatible with -o -; stdout is raw YAML")
	}
	target := root.Repo
	if c.Path != "" {
		target = c.Path
	}
	result, err := deps.App.PipelineScan(ctx, target, c.MaxPhases, c.MaxStages, c.Update)
	if err != nil {
		return err
	}
	if c.Output == "-" {
		if _, copyErr := io.Copy(deps.Out, bytes.NewReader(result.Data)); copyErr != nil {
			return fmt.Errorf("write scan to stdout: %w", copyErr)
		}
		return nil
	}

	output := c.Output
	if output == "" {
		output, err = spec.ScanOutputPath(target)
		if err != nil {
			return fmt.Errorf("resolve output path: %w", err)
		}
	}
	if outputErr := ensureScanOutput(output, result.UpdatePath, c.Force); outputErr != nil {
		return outputErr
	}
	if c.Force || samePath(output, result.UpdatePath) {
		err = atomicfile.Write(output, result.Data, 0o644)
	} else {
		err = atomicfile.WriteNew(output, result.Data, 0o644)
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", output, err)
	}
	return emitResult(kctx, root, deps, result.Root, map[string]any{
		outputKey:            output,
		"root":               result.Root,
		"updated":            result.UpdatePath != "",
		"outside_repository": isOutsideRoot(output, result.Root),
	})
}

func ensureScanOutput(output, update string, force bool) error {
	info, err := os.Stat(output)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("output path is a directory: %s", output)
		}
		if force || samePath(output, update) {
			return nil
		}
		return fmt.Errorf("spec file already exists: %s\n\nrefusing to overwrite. choose one:\n  --force               overwrite (destructive; previous content lost)\n  --update <name>       merge scan into existing spec, preserving hand-written fields\n  -o -                  print spec to stdout instead of writing", output)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat output: %w", err)
	}
	return nil
}

func samePath(left, right string) bool {
	if right == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func isOutsideRoot(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
