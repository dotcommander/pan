package review

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/dotcommander/pan/internal/analyze"
)

// ReportPaths returns snapshot paths that Pan's filesystem report fallback
// considers as text evidence. The snapshot confines discovery and File.Size
// bounds this final read to the already accepted per-file limit.
func ReportPaths(root string, files []analyze.File) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if reportPathExcluded(file.Path) || !reportTextPath(root, file) {
			continue
		}
		paths = append(paths, file.Path)
	}
	return paths
}

func reportTextPath(root string, file analyze.File) bool {
	if file.Size <= 0 {
		return false
	}
	absPath := filepath.Join(root, filepath.FromSlash(file.Path))
	rel, err := filepath.Rel(root, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	f, err := os.Open(absPath)
	if err != nil {
		return false
	}
	_, ok, err := readReportTextPrefix(f, file.Size)
	closeErr := f.Close()
	return err == nil && closeErr == nil && ok
}

// readReportTextPrefix mirrors Pan's bounded binary and UTF-8 gate. A
// final UTF-8 rune may cross the limit, so a small look-ahead distinguishes a
// valid truncation from invalid text without reading an unbounded file.
func readReportTextPrefix(reader io.Reader, maxBytes int64) (string, bool, error) {
	readLimit := maxBytes
	const maxInt64 = int64(^uint64(0) >> 1)
	if maxBytes <= maxInt64-utf8.UTFMax {
		readLimit += utf8.UTFMax
	}
	data, err := io.ReadAll(io.LimitReader(reader, readLimit))
	if err != nil {
		return "", false, err
	}
	if len(data) == 0 || strings.Contains(string(data[:min(len(data), 4096)]), "\x00") {
		return "", false, nil
	}
	truncated := int64(len(data)) > maxBytes
	if !truncated {
		return string(data), utf8.Valid(data), nil
	}
	bounded := data[:maxBytes]
	if utf8.Valid(bounded) {
		return string(bounded), true, nil
	}
	prefix, valid := trimIncompleteReportUTF8(bounded)
	if !valid {
		return "", false, nil
	}
	crossing := data[len(prefix):]
	if !utf8.FullRune(crossing) {
		return "", false, nil
	}
	if r, size := utf8.DecodeRune(crossing); r == utf8.RuneError && size == 1 {
		return "", false, nil
	}
	return string(prefix), true, nil
}

func trimIncompleteReportUTF8(data []byte) ([]byte, bool) {
	for offset := 0; offset < len(data); {
		_, size := utf8.DecodeRune(data[offset:])
		if size == 1 && data[offset] >= utf8.RuneSelf {
			if !utf8.FullRune(data[offset:]) {
				return data[:offset], true
			}
			return nil, false
		}
		offset += size
	}
	return data, true
}

func reportPathExcluded(filePath string) bool {
	for _, part := range strings.Split(filePath, "/") {
		switch part {
		case ".git", "node_modules", "vendor", "dist", "build", "target", "coverage", ".next", ".svelte-kit", ".venv", ".work":
			return true
		}
	}
	lower := strings.ToLower(filePath)
	for _, suffix := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip", ".gz", ".tar", ".mp4", ".mp3", ".lock", ".sum"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}
