package app

import (
	"context"

	"github.com/dotcommander/pan/internal/bench"
)

// BenchRetrieval scores Pan's retrieval and the BM25 baseline against an
// issue-to-file dataset over local git mirrors. The service supplies one
// bounded snapshot per instance checkout.
func (s Service) BenchRetrieval(ctx context.Context, options bench.RunOptions) (bench.BenchReport, error) {
	return bench.Run(ctx, s, options)
}
