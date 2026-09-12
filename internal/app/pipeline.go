package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dotcommander/pan/internal/pipeline/render"
	"github.com/dotcommander/pan/internal/pipeline/review"
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// PipelineRender loads the spec named by specArg and renders its HTML page.
// When outputPath is non-empty the page is written there atomically and the
// returned HTML is nil; otherwise the page is returned inline.
func (s Service) PipelineRender(specArg, repoRoot, outputPath string, input ...io.Reader) (specPath string, html []byte, err error) {
	loaded, specPath, err := loadPipelineSpec(specArg, input...)
	if err != nil {
		return "", nil, err
	}
	sourceRoot := spec.ProjectRootForSpec(loaded, specPath, repoRoot)
	if outputPath != "" {
		if renderErr := render.Render(loaded, outputPath, sourceRoot); renderErr != nil {
			return "", nil, fmt.Errorf("render: %w", renderErr)
		}
		return specPath, nil, nil
	}
	html, err = render.ToBytes(loaded, sourceRoot)
	if err != nil {
		return "", nil, fmt.Errorf("render: %w", err)
	}
	return specPath, html, nil
}

// PipelineValidate loads the spec named by specArg and returns its
// validation findings. A nil slice means the spec is valid; a non-nil error
// means the spec could not be loaded at all.
func (s Service) PipelineValidate(specArg, repoRoot string, input ...io.Reader) (specPath string, findings []error, err error) {
	loaded, specPath, err := loadPipelineSpec(specArg, input...)
	if err != nil {
		return "", nil, err
	}
	return specPath, validationFindings(loaded, specPath, repoRoot), nil
}

// validationFindings unwraps a joined validation error into one error per
// finding so callers can report each separately.
func validationFindings(s *spec.Spec, specPath, repoRoot string) []error {
	err := spec.ValidateWithRoot(s, specPath, repoRoot)
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		if errs := joined.Unwrap(); len(errs) > 0 {
			return errs
		}
	}
	return []error{err}
}

// PipelineReview loads the spec named by specArg, indexes the repository
// symbols, and reviews the spec for drift. The review is read-only.
func (s Service) PipelineReview(ctx context.Context, specArg, repoRoot string, input ...io.Reader) (specPath string, report *review.Report, err error) {
	loaded, specPath, err := loadPipelineSpec(specArg, input...)
	if err != nil {
		return "", nil, err
	}
	root := spec.ProjectRootForSpec(loaded, specPath, repoRoot)
	index := symbols.New(root, symbols.Config{})
	if err := index.Build(ctx); err != nil {
		return "", nil, fmt.Errorf("symbols build: %w", err)
	}
	return specPath, review.Review(ctx, loaded, root, index.Ranked()), nil
}

// loadPipelineSpec resolves a spec argument (literal path, bare name, or
// DataDir-resolved name) and loads it.
func loadPipelineSpec(specArg string, input ...io.Reader) (*spec.Spec, string, error) {
	if specArg == "-" {
		if len(input) == 0 || input[0] == nil {
			return nil, "", errors.New("stdin spec requires an input stream")
		}
		loaded, err := spec.LoadReader("stdin", input[0])
		return loaded, "-", err
	}
	if specArg == "" {
		return nil, "", errors.New("spec argument is required")
	}
	path, err := spec.Resolve(specArg)
	if err != nil {
		return nil, "", err
	}
	loaded, err := spec.Load(path)
	if err != nil {
		return nil, "", err
	}
	return loaded, path, nil
}
