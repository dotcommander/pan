package gitoutgoing

import (
	"context"
	"os/exec"
)

type commandFactoryKey struct{}

// commandFor keeps subprocess fixtures local to an inspection context.
func commandFor(ctx context.Context, args ...string) *exec.Cmd {
	if factory, ok := ctx.Value(commandFactoryKey{}).(func(...string) *exec.Cmd); ok {
		return factory(args...)
	}
	return exec.Command("git", args...)
}
