//go:build !windows

package atomicfile

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

func TestDirectorySyncIgnoresOnlyUnsupportedOperations(t *testing.T) {
	t.Parallel()
	for _, err := range []error{syscall.EINVAL, syscall.ENOTSUP} {
		if got := supportedDirSyncError(fmt.Errorf("sync: %w", err)); got != nil {
			t.Errorf("unsupported error = %v", got)
		}
	}
	for _, err := range []error{syscall.EIO, syscall.EBADF, syscall.EACCES} {
		if got := supportedDirSyncError(fmt.Errorf("sync: %w", err)); !errors.Is(got, err) {
			t.Errorf("operational error lost: %v", got)
		}
	}
}
