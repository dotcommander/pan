//go:build !windows

package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func replaceFile(tmp, dest string) error { return os.Rename(tmp, dest) }
func publishNew(tmp, dest string) error  { return os.Link(tmp, dest) }

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open output directory: %w", err)
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	// Some filesystems do not implement directory fsync. No other errors are ignored.
	syncErr = supportedDirSyncError(syncErr)
	return errors.Join(syncErr, closeErr)
}

func supportedDirSyncError(err error) error {
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}
