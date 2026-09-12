package app

import (
	"context"

	"github.com/dotcommander/pan/internal/lsp"
)

// LspStatus reports language-server coverage for one target repository
// from the configured language table. Detection is local and read-only:
// no server is started.
func (s Service) LspStatus(ctx context.Context, root string) (lsp.StatusReport, error) {
	return lsp.NewService(s.deps.Config.Lsp, s.deps.Config.Exclude).Status(ctx, absPath(root))
}

// LspQueryOptions names one bounded language-server query: the capability,
// the target file, and the 0-based line and UTF-16 column inside it.
type LspQueryOptions struct {
	Capability string
	File       string
	Line       int
	Column     int
}

// LspQuery answers one bounded language-server query for one file
// position. The result is always a typed capability answer; environment
// facts (missing server, unconfigured language) are reported inside the
// result, not as command errors.
func (s Service) LspQuery(ctx context.Context, root string, opts LspQueryOptions) (lsp.QueryResult, error) {
	return lsp.NewService(s.deps.Config.Lsp, s.deps.Config.Exclude).Query(ctx, absPath(root), lsp.QueryRequest{
		Capability: opts.Capability,
		File:       opts.File,
		Line:       opts.Line,
		Column:     opts.Column,
	})
}
