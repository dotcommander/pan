package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/dotcommander/pan/internal/app"
)

// ContextPackCmd accepts captured file content rather than a repository directory.
type ContextPackCmd struct {
	Input string `arg:"" name:"input" help:"Repomix JSON/parsable XML input; - reads stdin."`
}

func (c ContextPackCmd) Run(deps Deps, ctx context.Context) error {
	input := deps.In
	if c.Input != "-" {
		file, err := os.Open(c.Input)
		if err != nil {
			return fmt.Errorf("open pack input: %w", err)
		}
		defer file.Close()
		input = file
	}
	data, err := app.PackContext(ctx, input)
	if err != nil {
		return err
	}
	_, err = io.Copy(deps.Out, bytes.NewReader(data))
	return err
}
