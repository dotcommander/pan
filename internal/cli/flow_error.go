package cli

import (
	"errors"

	"github.com/alecthomas/kong"
)

// Keep the process failure while giving JSON consumers a parseable diagnostic.
func emitCallError(kctx *kong.Context, root *Root, deps Deps, selector string, err error) error {
	if root.Format != formatJSON {
		return err
	}
	result := map[string]any{"selector": selector}
	if emitErr := emitDiagnostics(kctx, root, deps, result, []string{err.Error()}); emitErr != nil {
		return errors.Join(err, emitErr)
	}
	return err
}
