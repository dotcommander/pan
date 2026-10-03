package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type failingStage struct {
	*os.File
	fault   string
	failure error
}

func (s *failingStage) Write(p []byte) (int, error) {
	if s.fault == "write" {
		return 0, s.failure
	}
	return s.File.Write(p)
}
func (s *failingStage) Sync() error {
	if s.fault == "sync" {
		return s.failure
	}
	return s.File.Sync()
}
func (s *failingStage) Close() error {
	err := s.File.Close()
	if s.fault == "close" {
		return errors.Join(err, s.failure)
	}
	return err
}

func TestStreamingFailurePublicationAndCleanup(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"write", "sync", "close", "publish", "directory-sync"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			dest := filepath.Join(t.TempDir(), "backup.tar.gz")
			failure := errors.New("injected " + fault)
			ops := fileOps{
				create: func(dir, pattern string) (stagedFile, error) {
					f, err := os.CreateTemp(dir, pattern)
					if err != nil {
						return nil, err
					}
					return &failingStage{File: f, fault: fault, failure: failure}, nil
				},
				syncDir: func(dir string) error {
					if fault == "directory-sync" {
						return failure
					}
					return syncDir(dir)
				},
			}
			publish := func(tmp, dest string) error {
				if fault == "publish" {
					return failure
				}
				return publishNew(tmp, dest)
			}
			err := streamFileWith(dest, 0o600, publish, func(w io.Writer) error { _, err := w.Write([]byte("complete")); return err }, ops)
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			var published *PublishedError
			if errors.As(err, &published) != (fault == "directory-sync") {
				t.Fatalf("publication classification = %v", err)
			}
			data, readErr := os.ReadFile(dest)
			if fault == "directory-sync" {
				if readErr != nil || string(data) != "complete" {
					t.Fatalf("published output = %q, %v", data, readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("unpublished output exists: %v", readErr)
			}
			assertNoTemps(t, dest)
		})
	}
}

func TestStreamNewRefusesCollisionAndRejectsSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "original")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Write(link, []byte("replacement"), 0o600); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := StreamNew(target, 0o600, func(w io.Writer) error { _, err := io.WriteString(w, "replacement"); return err }); err == nil {
		t.Fatal("existing destination overwritten")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatalf("original = %q %v", data, err)
	}
	assertNoTemps(t, target)
}
