package atomicfile

import "golang.org/x/sys/windows"

func moveFile(tmp, dest string, flags uint32) error {
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dest)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, flags|windows.MOVEFILE_WRITE_THROUGH)
}
func replaceFile(tmp, dest string) error {
	return moveFile(tmp, dest, windows.MOVEFILE_REPLACE_EXISTING)
}
func publishNew(tmp, dest string) error { return moveFile(tmp, dest, 0) }

// Windows does not support fsync on Go directory handles. MoveFileEx uses
// WRITE_THROUGH, after the staged file has been flushed and closed.
func syncDir(string) error { return nil }
