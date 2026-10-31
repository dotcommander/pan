package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

// verifyManifest checks the same whole-tree stamps and content hashes for both
// callers. Only LoadValidated captures source bytes; Inspect reuses one buffer.
func verifyManifest(ctx context.Context, root string, cfg config.Config, entry Entry, capture bool) (map[string][]byte, map[string]analyze.FileStamp, string) {
	before, err := analyze.Stamps(ctx, root, cfg)
	if err != nil {
		return nil, nil, "stamp_failed"
	}
	if stale, reason := stampsStale(entry.Stamps, before); stale {
		return nil, nil, reason
	}
	var contents map[string][]byte
	var buffer []byte
	if capture {
		contents = make(map[string][]byte, len(entry.Manifest))
	} else {
		buffer = make([]byte, 32*1024)
	}
	for path, want := range entry.Manifest {
		file := filepath.Join(root, filepath.FromSlash(path))
		var sum [sha256.Size]byte
		if capture {
			data, readErr := os.ReadFile(file)
			if readErr != nil {
				return nil, nil, "content_unavailable"
			}
			sum = sha256.Sum256(data)
			contents[path] = data
		} else {
			var readErr error
			sum, readErr = streamHash(file, buffer)
			if readErr != nil {
				return nil, nil, "content_unavailable"
			}
		}
		if hex.EncodeToString(sum[:]) != want {
			return nil, nil, reasonContentChanged
		}
	}
	after, err := analyze.Stamps(ctx, root, cfg)
	if err != nil || analyzeStampsChanged(before, after) {
		return nil, nil, "repository_changed_during_capture"
	}
	return contents, after, ""
}

func streamHash(path string, buffer []byte) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	hash := sha256.New()
	// Hide os.File's WriteTo so CopyBuffer actually reuses our buffer.
	_, readErr := io.CopyBuffer(hash, struct{ io.Reader }{file}, buffer)
	closeErr := file.Close()
	if readErr != nil {
		return [sha256.Size]byte{}, readErr
	}
	if closeErr != nil {
		return [sha256.Size]byte{}, closeErr
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}
