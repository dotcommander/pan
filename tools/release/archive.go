package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxArchiveBytes = 128 << 20

func (w *releaseWorkflow) archiveVerify(ctx context.Context) error {
	archiveURL := w.archiveURL()
	content, err := downloadArchive(ctx, archiveURL)
	if err != nil {
		return err
	}
	if err := verifyReleaseArchive(content, w.version); err != nil {
		return err
	}
	hash := sha256.Sum256(content)
	w.state.ArchiveSHA256 = hex.EncodeToString(hash[:])
	fmt.Fprintf(w.proc.stdout, "archive %s sha256 %s\n", archiveURL, w.state.ArchiveSHA256)
	return nil
}

func (w *releaseWorkflow) archiveURL() string {
	return "https://github.com/" + sourceRepository + "/archive/refs/tags/" + w.tag + ".tar.gz"
}

func downloadArchive(ctx context.Context, archiveURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err == nil && response.StatusCode == http.StatusOK {
			content, readErr := io.ReadAll(io.LimitReader(response.Body, maxArchiveBytes+1))
			closeErr := response.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if closeErr != nil {
				lastErr = closeErr
			} else if len(content) > maxArchiveBytes {
				return nil, fmt.Errorf("archive exceeds %d bytes", maxArchiveBytes)
			} else {
				return content, nil
			}
		} else {
			if response != nil {
				io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
				response.Body.Close()
				lastErr = fmt.Errorf("archive returned HTTP %d", response.StatusCode)
			} else {
				lastErr = err
			}
		}
		if attempt < 5 {
			delay := time.Duration(1<<attempt) * time.Second
			if delay > 8*time.Second {
				delay = 8 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, fmt.Errorf("download %s: %w", archiveURL, lastErr)
}

func verifyReleaseArchive(content []byte, version string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("open release archive: %w", err)
	}
	defer gzipReader.Close()

	prefix := "pan-" + version + "/"
	requiredFiles := map[string]bool{
		"cmd/pan/main.go":     false,
		"internal/cli/cli.go": false,
		"go.mod":              false,
		"LICENSE":             false,
	}
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read release archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || !strings.HasPrefix(header.Name, prefix) {
			continue
		}
		relative := strings.TrimPrefix(header.Name, prefix)
		if _, needed := requiredFiles[relative]; needed {
			data, err := readTarFile(tarReader, header.Size)
			if err != nil {
				return err
			}
			requiredFiles[relative] = true
			if relative == "internal/cli/cli.go" {
				observed, parseErr := binaryVersionFromContent(data)
				if parseErr != nil || observed != version {
					return fmt.Errorf("archive binaryVersion %q does not match release version %q", observed, version)
				}
			}
		}
	}
	for name, present := range requiredFiles {
		if !present {
			return fmt.Errorf("archive missing %s", name)
		}
	}
	return nil
}

func readTarFile(reader io.Reader, size int64) ([]byte, error) {
	if size < 0 || size > 8<<20 {
		return nil, fmt.Errorf("archive entry size %d is outside the supported range", size)
	}
	content, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) != size {
		return nil, errors.New("archive entry length mismatch")
	}
	return content, nil
}
