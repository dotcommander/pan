package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/alecthomas/kong"
)

// ContextImpactCmd is the file-focused Pan impact packet.
type ContextImpactCmd struct {
	File string `arg:"" help:"Repository-relative or absolute source file."`
}

// Validate rejects a blank target before building a snapshot.
func (c ContextImpactCmd) Validate() error {
	if strings.TrimSpace(c.File) == "" {
		return errors.New("impact file is required")
	}
	return nil
}

// Run executes `pan context impact`.
func (c ContextImpactCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	snap, result, err := deps.App.FileImpact(ctx, root.Repo, c.File)
	if err != nil {
		return err
	}
	return emit(kctx, root, deps, snap, result)
}
