package clean

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

type closeFailTar struct {
	tarSink
	failure error
}

func (w closeFailTar) Close() error { return errors.Join(w.tarSink.Close(), w.failure) }

type closeFailGzip struct {
	io.WriteCloser
	failure error
}

func (w closeFailGzip) Close() error { return errors.Join(w.WriteCloser.Close(), w.failure) }

type failingArchiveOutput struct{ failure error }

func (w failingArchiveOutput) Write([]byte) (int, error) { return 0, w.failure }

func TestArchiveLayerFailuresLeaveNoPublishedRecovery(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"write", "tar-close", "gzip-close"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeTestFile(t, root, "original", "recoverable")
			rootFS, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rootFS.Close() })
			dest := filepath.Join(root, "failed.tar.gz")
			failure := errors.New("archive " + phase)
			err = atomicfile.StreamNew(dest, filePerm, func(out io.Writer) error {
				layers := func(out io.Writer) (tarSink, io.WriteCloser) {
					if phase == "write" {
						out = failingArchiveOutput{failure: failure}
					}
					tw, gz := defaultArchiveLayers(out)
					if phase == "tar-close" {
						tw = closeFailTar{tarSink: tw, failure: failure}
					}
					if phase == "gzip-close" {
						gz = closeFailGzip{WriteCloser: gz, failure: failure}
					}
					return tw, gz
				}
				return writeArchivePayload(rootFS, root, []string{"original"}, out, layers)
			})
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("published failed archive: %v", err)
			}
			temps, err := filepath.Glob(filepath.Join(root, ".pan-*.tmp"))
			if err != nil || len(temps) != 0 {
				t.Fatalf("staging leftovers = %v, %v", temps, err)
			}
			data, err := os.ReadFile(filepath.Join(root, "original"))
			if err != nil || string(data) != "recoverable" {
				t.Fatalf("source changed: %q %v", data, err)
			}
		})
	}
}

// This portable fixture is intended for native Windows execution as well.
func TestBackupPortableNestedContentsAndContainment(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, "nested/child/source.txt", "recoverable")
	rel, count, err := CreateBackup(root, ".work/archive", []string{"nested"}, applyNow())
	if err != nil || count != 1 {
		t.Fatalf("backup = %q, %d, %v", rel, count, err)
	}
	archive, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	gz, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gz.Close() })
	tr := tar.NewReader(gz)
	found := false
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "nested/child/source.txt" {
			data, err := io.ReadAll(io.LimitReader(tr, 64))
			if err != nil || string(data) != "recoverable" {
				t.Fatalf("archive content = %q %v", data, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("source content absent from archive")
	}
	headers := readBackupHeaders(t, filepath.Join(root, filepath.FromSlash(rel)))
	if headers["nested/child/source.txt"] == nil {
		t.Fatalf("missing nested source: %v", headerNames(headers))
	}
	for name := range headers {
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, "../") {
			t.Errorf("unsafe archive name %q", name)
		}
	}
	for _, target := range []string{".", `..\outside`, `C:\outside`, `.git\config`} {
		if _, _, err := CreateBackup(root, ".work/archive", []string{target}, applyNow()); err == nil {
			t.Errorf("accepted unsafe target %q", target)
		}
	}
}
