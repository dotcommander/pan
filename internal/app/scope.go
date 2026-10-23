package app

import (
	"slices"

	"github.com/dotcommander/pan/internal/config"
)

// WithExclusions derives an invocation-local scope without changing the
// receiver, configured exclusions, or cache/source collaborators.
func (s Service) WithExclusions(paths []string) Service {
	if len(paths) == 0 {
		return s
	}
	cfg := s.deps.Config.Normalized()
	cfg.Exclude = config.NormalizeExcludes(slices.Concat(cfg.Exclude, paths))
	s.deps.Config = cfg
	return s
}
