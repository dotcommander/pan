// Package scan walks a Go module using Pan and produces a Pan spec.
package scan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// scanMaxFileSize raises Pan's default 50KB discovery cap so normal large
// source files (e.g. a 1400-line, ~50KB Go file) are scanned and represented.
// Files above this are reported in coverage as skipped — never silently dropped.
const scanMaxFileSize = 1_000_000

const (
	// DefaultMaxPhases is the shared scan phase cap used when no explicit
	// max-phases value is supplied.
	DefaultMaxPhases = 9
	// DefaultMaxStages is the shared per-phase stage cap used when no explicit
	// max-stages value is supplied.
	DefaultMaxStages = 7
)

// Config controls how the scanner maps source structure to spec phases.
type Config struct {
	MaxPhases int // maximum number of phases (default DefaultMaxPhases)
	MaxStages int // maximum stages per phase (default DefaultMaxStages)
}

// Scan walks root using Pan, then maps the ranked file graph to a spec.Spec.
// When the repo follows the dispatcher pattern (cmd/<name>/main.go + ≥3
// internal/commands/*.go with newXCmd constructors), the output uses
// Spec.Phases for Boot/Dispatch prelude and Spec.Commands for per-command lanes.
func Scan(ctx context.Context, root string, cfg Config) (*spec.Spec, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	cfg = applyDefaults(cfg)

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	m := symbols.New(absRoot, symbols.Config{MaxTokens: 0, MaxFileSize: scanMaxFileSize})
	if err := m.Build(ctx); err != nil {
		return nil, fmt.Errorf("scan build: %w", err)
	}

	ranked := m.Ranked()
	if len(ranked) == 0 {
		return nil, fmt.Errorf("scan: no Go files found in %s", root)
	}

	base := filepath.Base(absRoot)
	title := strings.ToUpper(base[:1]) + base[1:]

	// Try dispatcher shape first.
	if info, ok := detectDispatchShape(absRoot, ranked); ok {
		return buildDispatcherSpec(absRoot, title, ranked, cfg, info)
	}

	// Flat shape (current code path — unchanged).
	return buildFlatSpec(absRoot, title, ranked, cfg)
}

// ValidateConfig checks scan sizing inputs before any repository work starts.
func ValidateConfig(cfg Config) error {
	if cfg.MaxPhases < 0 {
		return errors.New("scan: max phases must be non-negative")
	}
	if cfg.MaxStages < 0 {
		return errors.New("scan: max stages must be non-negative")
	}
	return nil
}

func applyDefaults(cfg Config) Config {
	if cfg.MaxPhases == 0 {
		cfg.MaxPhases = DefaultMaxPhases
	}
	if cfg.MaxStages == 0 {
		cfg.MaxStages = DefaultMaxStages
	}
	return cfg
}

// buildFlatSpec is the original flat-shape code path: all phases in Spec.Phases.
func buildFlatSpec(absRoot, title string, ranked []symbols.RankedFile, cfg Config) (*spec.Spec, error) {
	// Phase refinement runs through the shared phasePipeline. detectStores and
	// detectSources run after the chain, in that order, because detectStores
	// returns (stores, phases) and the original ordering places detectSources
	// last (after detectStores mutates the phase files).
	phases := runFlatChain(absRoot, ranked, cfg)
	stores, phases := detectStores(absRoot, phases)
	phases = detectSources(absRoot, phases)

	breadcrumb := buildBreadcrumb(absRoot, phases)
	coverage := computeCoverage(ranked, phases, absRoot, scanMaxFileSize)

	return &spec.Spec{
		Title:      title,
		Breadcrumb: breadcrumb,
		Phases:     phases,
		Stores:     stores,
		Coverage:   coverage,
	}, nil
}
