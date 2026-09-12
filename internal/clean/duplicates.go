package clean

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// duplicateHashThreshold is the size floor for basename-collision and
// content-hash duplicate detection; smaller files collide too easily.
const duplicateHashThreshold = 4096

// FindDuplicates detects duplicate files and sets FileInfo.Duplicate using
// three signals: backup-suffix patterns, basename collisions by size, and
// same-size content hashes. Detection is read-only.
func FindDuplicates(files []FileInfo) {
	byPath, byBase := indexFiles(files)
	markBackupSuffixes(files, byPath)
	markBasenameCollisions(byBase)
	markContentHashDuplicates(files)
}

// indexFiles maps non-directory, non-symlink files by path and by base
// name.
func indexFiles(files []FileInfo) (map[string]*FileInfo, map[string][]*FileInfo) {
	byPath := make(map[string]*FileInfo, len(files))
	byBase := make(map[string][]*FileInfo)
	for i := range files {
		f := &files[i]
		if f.IsDir || f.IsSymlink {
			continue
		}
		byPath[f.RelPath] = f
		base := filepath.Base(f.RelPath)
		byBase[base] = append(byBase[base], f)
	}
	return byPath, byBase
}

// markBackupSuffixes applies signal 1: backup-suffix patterns naming a
// nearby original.
func markBackupSuffixes(files []FileInfo, byPath map[string]*FileInfo) {
	suffixes := backupSuffixes()
	for i := range files {
		f := &files[i]
		if f.IsDir || f.IsSymlink || f.Duplicate != "" {
			continue
		}
		if orig := findOriginal(f.RelPath, suffixes, byPath); orig != "" {
			f.Duplicate = orig
		}
	}
}

// backupSuffixes returns filename stems that indicate a backup copy of a
// nearby original.
func backupSuffixes() []string {
	return []string{"-v2", "-old", "-backup", "-copy", "-draft", ".orig", ".backup"}
}

// markBasenameCollisions applies signal 2: same-name files with identical
// sizes, ignoring names that conventionally repeat across directories.
func markBasenameCollisions(byBase map[string][]*FileInfo) {
	multi := commonMultiNames()
	for baseName, group := range byBase {
		if len(group) < 2 || multi[baseName] {
			continue
		}
		markSizeCollisions(group)
	}
}

// commonMultiNames returns filenames that conventionally appear in
// multiple directories within a project and are not meaningful duplicates.
func commonMultiNames() map[string]bool {
	return map[string]bool{
		"main.go": true, "main.ts": true, "main.py": true, "main.rs": true,
		"index.ts": true, "index.js": true, "index.tsx": true, "index.jsx": true,
		"index.html": true, "index.css": true,
		"README.md": true, "CHANGELOG.md": true, "LICENSE": true,
		makefileName: true, "Taskfile.yml": true, "Dockerfile": true,
		".gitignore": true, ".env": true, ".env.example": true,
		"types.go": true, "types.ts": true, "utils.go": true, "utils.ts": true,
		"config.go": true, "config.ts": true, "config.yaml": true, "config.json": true,
		"test_helpers.go": true, "testdata": true,
	}
}

func markSizeCollisions(group []*FileInfo) {
	bySize := make(map[int64][]*FileInfo)
	for _, f := range group {
		if f.Duplicate == "" {
			bySize[f.Size] = append(bySize[f.Size], f)
		}
	}
	for _, matches := range bySize {
		if len(matches) < 2 || matches[0].Size <= duplicateHashThreshold {
			continue
		}
		markShortestAsDuplicate(matches)
	}
}

// markContentHashDuplicates applies signal 3: same-size files with
// different names whose hashed prefixes match.
func markContentHashDuplicates(files []FileInfo) {
	bySize := make(map[int64][]*FileInfo)
	for i := range files {
		f := &files[i]
		if f.IsDir || f.IsSymlink || f.Duplicate != "" || f.Size <= duplicateHashThreshold {
			continue
		}
		bySize[f.Size] = append(bySize[f.Size], f)
	}
	for _, group := range bySize {
		markHashCollisions(group)
	}
}

func markHashCollisions(group []*FileInfo) {
	if len(group) < 2 {
		return
	}
	byHash := make(map[string][]*FileInfo)
	for _, f := range group {
		if h := hashPrefix(f.Path); h != "" {
			byHash[h] = append(byHash[h], f)
		}
	}
	for _, matches := range byHash {
		if len(matches) >= 2 {
			markShortestAsDuplicate(matches)
		}
	}
}

// markShortestAsDuplicate picks the file with the shortest relative path as
// canonical and marks the others as duplicates of it.
func markShortestAsDuplicate(matches []*FileInfo) {
	shortest := matches[0]
	for _, f := range matches[1:] {
		if len(f.RelPath) < len(shortest.RelPath) {
			shortest = f
		}
	}
	for _, f := range matches {
		if f != shortest && f.Duplicate == "" {
			f.Duplicate = shortest.RelPath
		}
	}
}

// hashPrefix returns the hex SHA-256 of the first 4 KiB of a file.
func hashPrefix(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, copyErr := io.Copy(h, io.LimitReader(f, 4096)); copyErr != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// findOriginal checks whether rel carries a backup suffix and returns the
// relative path of the corresponding original when it exists.
func findOriginal(rel string, suffixes []string, byPath map[string]*FileInfo) string {
	dir := filepath.Dir(rel)
	base := filepath.Base(rel)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	for _, suffix := range suffixes {
		if !strings.HasSuffix(stem, suffix) {
			continue
		}
		origBase := strings.TrimSuffix(stem, suffix) + ext
		origRel := origBase
		if dir != "." {
			origRel = dir + string(os.PathSeparator) + origBase
		}
		if _, ok := byPath[origRel]; ok {
			return origRel
		}
	}
	return ""
}
