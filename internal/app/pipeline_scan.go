package app

import (
	"context"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// PipelineScanResult is the serialised pipeline spec and the repository root
// used to produce it. UpdatePath is non-empty when --update supplied a spec.
type PipelineScanResult struct {
	Data       []byte
	Root       string
	UpdatePath string
}

// PipelineScan scans root into a pipeline spec. When update is supplied, its
// human-authored fields are merged into the fresh scan before serialisation.
func (s Service) PipelineScan(ctx context.Context, root string, maxPhases, maxStages int, update string) (PipelineScanResult, error) {
	scanned, err := scan.Scan(ctx, root, scan.Config{MaxPhases: maxPhases, MaxStages: maxStages})
	if err != nil {
		return PipelineScanResult{}, err
	}
	result := PipelineScanResult{}
	if update != "" {
		result.UpdatePath, err = spec.Resolve(update)
		if err != nil {
			return PipelineScanResult{}, fmt.Errorf("resolve --update: %w", err)
		}
		existing, loadErr := spec.Load(result.UpdatePath)
		if loadErr != nil {
			return PipelineScanResult{}, fmt.Errorf("load existing spec: %w", loadErr)
		}
		scanned = scan.Merge(existing, scanned)
	}
	result.Root, err = filepath.Abs(root)
	if err != nil {
		return PipelineScanResult{}, fmt.Errorf("resolve root: %w", err)
	}
	// Merge deliberately omits Root, so set it after all merge work.
	scanned.Root = result.Root
	result.Data, err = yaml.Marshal(scanned)
	if err != nil {
		return PipelineScanResult{}, fmt.Errorf("marshal: %w", err)
	}
	return result, nil
}
