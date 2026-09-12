package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/lsp"
)

// ContextLspCmd groups bounded language-server queries. Status is pure
// local detection; the query leaves answer with typed capability results,
// so a missing or unconfigured server is reported data, not a command
// failure. Queries spawn only local server subprocesses; pan never
// contacts a provider or the network.
type ContextLspCmd struct {
	Status  ContextLspStatusCmd  `cmd:"" help:"Language-server availability for this repository."`
	Refs    ContextLspRefsCmd    `cmd:"" help:"Symbol references at a file position."`
	Def     ContextLspDefCmd     `cmd:"" help:"Symbol definition at a file position."`
	Hover   ContextLspHoverCmd   `cmd:"" help:"Hover detail at a file position."`
	Symbols ContextLspSymbolsCmd `cmd:"" help:"Document symbols for one file."`
}

// ContextLspStatusCmd is `pan context lsp status`.
type ContextLspStatusCmd struct{}

// Run executes `pan context lsp status`.
func (ContextLspStatusCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	report, err := deps.App.LspStatus(ctx, root.Repo)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), report)
}

// lspPositionArgs accepts Pan-compatible source positions: a 1-based
// line and an identifier on that line. A numeric third argument retains Pan's
// legacy 0-based UTF-16 column form.
type lspPositionArgs struct {
	File     string `arg:"" type:"path" help:"Source file path."`
	Line     int    `arg:"" help:"1-based source line (or 0-based with a numeric column)."`
	Selector string `arg:"" help:"Identifier on the source line, or legacy 0-based UTF-16 column."`
}

func (a lspPositionArgs) Validate() error {
	if a.Line < 0 {
		return errors.New("line must not be negative")
	}
	return nil
}

func (a lspPositionArgs) options(root, capability string) (app.LspQueryOptions, error) {
	file := a.File
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	if column, err := strconv.Atoi(a.Selector); err == nil {
		if column < 0 {
			return app.LspQueryOptions{}, errors.New("column must not be negative")
		}
		return app.LspQueryOptions{Capability: capability, File: file, Line: a.Line, Column: column}, nil
	}
	line, column, err := lsp.SourcePosition(file, a.Line, a.Selector)
	if err != nil {
		return app.LspQueryOptions{}, err
	}
	return app.LspQueryOptions{Capability: capability, File: file, Line: line, Column: column}, nil
}

// ContextLspRefsCmd is `pan context lsp refs`.
type ContextLspRefsCmd struct{ lspPositionArgs }

// ContextLspDefCmd is `pan context lsp def`.
type ContextLspDefCmd struct{ lspPositionArgs }

// ContextLspHoverCmd is `pan context lsp hover`.
type ContextLspHoverCmd struct{ lspPositionArgs }

// ContextLspSymbolsCmd is `pan context lsp symbols`.
type ContextLspSymbolsCmd struct {
	File string `arg:"" type:"path" help:"Source file path."`
}

// runLspQuery executes one capability query and emits its typed result.
func runLspQuery(kctx *kong.Context, root *Root, deps Deps, ctx context.Context, opts app.LspQueryOptions) error {
	result, err := deps.App.LspQuery(ctx, root.Repo, opts)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), result)
}

// Run executes `pan context lsp refs`.
func (c ContextLspRefsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	opts, err := c.options(root.Repo, lsp.CapabilityRefs)
	if err != nil {
		return err
	}
	return runLspQuery(kctx, root, deps, ctx, opts)
}

// Run executes `pan context lsp def`.
func (c ContextLspDefCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	opts, err := c.options(root.Repo, lsp.CapabilityDef)
	if err != nil {
		return err
	}
	return runLspQuery(kctx, root, deps, ctx, opts)
}

// Run executes `pan context lsp hover`.
func (c ContextLspHoverCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	opts, err := c.options(root.Repo, lsp.CapabilityHover)
	if err != nil {
		return err
	}
	return runLspQuery(kctx, root, deps, ctx, opts)
}

// Run executes `pan context lsp symbols`.
func (c ContextLspSymbolsCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	return runLspQuery(kctx, root, deps, ctx, app.LspQueryOptions{Capability: lsp.CapabilitySymbols, File: c.File})
}
