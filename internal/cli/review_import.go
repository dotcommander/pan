package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/dotcommander/pan/internal/app"
)

// ReviewImportCmd consumes explicit captures; it never starts their producers.
type ReviewImportCmd struct {
	Format string `arg:"" name:"tool" enum:"ast-grep,jscpd,scip" help:"Upstream report format."`
	Input  string `arg:"" name:"input" help:"Captured report file; - reads stdin."`
}

func (c ReviewImportCmd) Run(root *Root, deps Deps, ctx context.Context) error {
	input := deps.In
	if c.Input != "-" {
		file, err := os.Open(c.Input)
		if err != nil {
			return fmt.Errorf("open tool report: %w", err)
		}
		defer file.Close()
		input = file
	}
	data, err := app.ImportToolReport(ctx, c.Format, input, root.Format)
	if err != nil {
		return err
	}
	_, err = io.Copy(deps.Out, bytes.NewReader(data))
	return err
}
