package buildinfo

import "testing"

func TestReadDoesNotPanic(t *testing.T) {
	t.Parallel()
	info := Read()
	// Fields are best-effort under `go test` (no vcs stamp); just exercise Read.
	_ = info.GoVersion
}
