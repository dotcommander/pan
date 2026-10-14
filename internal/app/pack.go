package app

import (
	"context"
	"io"

	"github.com/dotcommander/pan/internal/pack"
)

// PackContext normalizes a captured inventory without loading a source snapshot.
func PackContext(ctx context.Context, input io.Reader) ([]byte, error) {
	return pack.Build(ctx, input, pack.Limits{})
}
