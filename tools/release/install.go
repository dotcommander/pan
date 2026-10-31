package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// installSource enforces local installation before the tag or push phases.
// The install recipe refuses to replace an unrelated executable or symlink.
func (w *releaseWorkflow) installSource(ctx context.Context) error {
	if err := w.proc.stream(ctx, w.root, nil, "just", "install"); err != nil {
		return err
	}
	output, err := w.proc.capture(ctx, w.root, nil, filepath.Join(w.root, "pan"), "--version")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(output, "pan "+w.tag+" schema ") {
		return fmt.Errorf("installed binary version %q does not match %s", output, w.tag)
	}
	fmt.Fprintln(w.proc.stdout, output)
	return nil
}
