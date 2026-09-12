package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dotcommander/pan/internal/pipeline/impact"
	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// ImpactResult holds a resolved spec and its blast-radius analysis.
type ImpactResult struct {
	SpecPath   string
	SourceRoot string
	Analysis   *impact.Result
}

// PipelineImpact resolves or derives a spec, then analyzes the target's blast radius.
func (s Service) PipelineImpact(ctx context.Context, target, specArg, repoRoot string) (ImpactResult, error) {
	loaded, resolvedSpecPath, loadErr := s.impactSpec(ctx, specArg, repoRoot)
	if loadErr != nil {
		return ImpactResult{}, loadErr
	}
	sourceRoot := spec.ProjectRootForSpec(loaded, resolvedSpecPath, repoRoot)
	index := symbols.New(sourceRoot, symbols.Config{})
	var ranked []symbols.RankedFile
	if buildErr := index.Build(ctx); buildErr == nil {
		ranked = index.Ranked()
	}
	analysis, err := impact.Analyze(ctx, loaded, sourceRoot, target, ranked)
	if err != nil {
		return ImpactResult{}, fmt.Errorf("impact analysis: %w", err)
	}
	return ImpactResult{SpecPath: resolvedSpecPath, SourceRoot: sourceRoot, Analysis: analysis}, nil
}

func (s Service) impactSpec(ctx context.Context, specArg, repoRoot string) (*spec.Spec, string, error) {
	if specArg != "" {
		loaded, path, err := loadPipelineSpec(specArg)
		if err != nil {
			return nil, "", err
		}
		return loaded, path, nil
	}
	root := repoRoot
	if root == "" {
		root = "."
	}
	candidate := filepath.Join(root, "pan.yaml")
	if _, err := os.Stat(candidate); err == nil {
		loaded, path, loadErr := loadPipelineSpec(candidate)
		return loaded, path, loadErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("inspect impact spec %s: %w", candidate, err)
	}
	if repoRoot == "" {
		dataDir, err := spec.DataDir()
		if err != nil {
			return nil, "", fmt.Errorf("resolve impact spec directory: %w", err)
		}
		cached := filepath.Join(dataDir, spec.ProjectName(root)+".yaml")
		if _, statErr := os.Stat(cached); statErr == nil {
			loaded, path, loadErr := loadPipelineSpec(cached)
			return loaded, path, loadErr
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, "", fmt.Errorf("inspect impact spec %s: %w", cached, statErr)
		}
	}
	loaded, err := scan.Scan(ctx, root, scan.Config{})
	if err != nil {
		return nil, "", fmt.Errorf("scan repository for impact: %w", err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, "", fmt.Errorf("resolve repository: %w", err)
	}
	loaded.Root = absRoot
	return loaded, candidate, nil
}
