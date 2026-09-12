package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/retrieval"
	"github.com/dotcommander/pan/internal/scan"
)

// FileImpact builds the evidence-backed impact packet for one source file.
func (s Service) FileImpact(ctx context.Context, root, file string) (analyze.Snapshot, retrieval.ImpactResult, error) {
	snap, ranked, err := s.ranked(ctx, root, ranking.Options{})
	if err != nil {
		return analyze.Snapshot{}, retrieval.ImpactResult{}, err
	}
	rel, err := impactRelativePath(snap.Root, file)
	if err != nil {
		return snap, retrieval.ImpactResult{}, err
	}
	result, ok := retrieval.Impact(ranked, rel)
	if !ok {
		return snap, retrieval.ImpactResult{}, fmt.Errorf("file %q not found in analyzed snapshot", rel)
	}
	effects, err := scan.Effects(ctx, snap, 0)
	if err != nil {
		return snap, retrieval.ImpactResult{}, err
	}
	result.Boundaries = scan.EffectBoundaries(effects, rel)
	return snap, result, nil
}

func impactRelativePath(root, file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", errors.New("file is required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fileAbs := file
	if !filepath.IsAbs(fileAbs) {
		fileAbs = filepath.Join(rootAbs, fileAbs)
	}
	rel, err := filepath.Rel(rootAbs, fileAbs)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q is outside repository root", file)
	}
	return filepath.ToSlash(filepath.Clean(rel)), nil
}
