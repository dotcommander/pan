package cli

import (
	"bytes"
	"errors"
	"io"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

func writePipelineDocument(root *Root, deps Deps, data []byte) error {
	if root.Format == formatJSON {
		return errors.New("raw document stdout cannot be combined with --format json")
	}
	_, err := io.Copy(deps.Out, bytes.NewReader(data))
	return err
}

func (c FlowReviewCmd) emit(kctx *kong.Context, root *Root, deps Deps, markdown string, result map[string]any) error {
	output := c.Output
	if output == "-" {
		return writePipelineDocument(root, deps, []byte(markdown))
	}
	if output != "" {
		if err := atomicfile.Write(output, []byte(markdown), 0o600); err != nil {
			return err
		}
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), result)
}
