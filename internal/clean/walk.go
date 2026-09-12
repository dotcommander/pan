package clean

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// walker collects the read-only inventory for one scan.
type walker struct {
	root             string
	rootDepth        int
	maxDepth         int
	excludes         []string
	archiveRel       string
	noDescend        map[string]bool
	ignoreFile       string
	files            []FileInfo
	dirs             map[string]bool
	dirsWithChildren map[string]bool
	nestedRepos      map[string]bool
}

// Walk traverses the scan root within the configured depth, recording files,
// directories, symlinks, and empty directories. It skips .git entirely,
// detects and excludes nested repositories, records configured delete
// directories (cache trees) as entries without descending, never descends
// into the cleanup archive workspace, and honors the configured exclusion
// list. The walk is read-only.
func Walk(opts Options) ([]FileInfo, error) {
	root := opts.Root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if _, err := os.Stat(root); err != nil || !isDir(root) {
		return nil, fmt.Errorf("scan root must be a directory: %s", root)
	}
	w := newWalker(root, opts)
	if err := filepath.WalkDir(root, w.visit); err != nil {
		return nil, err
	}
	return w.finish(), nil
}

func newWalker(root string, opts Options) *walker {
	return &walker{
		root:             root,
		rootDepth:        pathDepth(root),
		maxDepth:         opts.maxDepth(),
		excludes:         normalizeExcludes(opts.Exclude),
		archiveRel:       slashClean(opts.Rules.ArchiveDir),
		noDescend:        toSet(opts.Rules.DeleteDirectories),
		ignoreFile:       opts.Rules.IgnoreFile,
		dirs:             make(map[string]bool),
		dirsWithChildren: make(map[string]bool),
		nestedRepos:      make(map[string]bool),
	}
}

// visit handles one walked path: .git entries record nested repositories,
// the root itself is skipped, and everything else delegates to the entry
// rules.
func (w *walker) visit(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return fmt.Errorf("walk %s: %w", path, err)
	}
	if d.Name() == gitDirName {
		return w.visitGitDir(path, d)
	}
	if path == w.root {
		return nil
	}
	return w.visitEntry(path, d)
}

// visitGitDir prunes one .git entry, remembering a nested repository when
// the .git directory is not the root's own.
func (w *walker) visitGitDir(path string, d fs.DirEntry) error {
	if parent := filepath.Dir(path); parent != w.root {
		w.nestedRepos[parent] = true
	}
	if d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

func (w *walker) visitEntry(path string, d fs.DirEntry) error {
	relPath, relErr := filepath.Rel(w.root, path)
	if relErr != nil {
		return fmt.Errorf("resolve %s: %w", path, relErr)
	}
	relSlash := filepath.ToSlash(relPath)
	if w.skipsEntry(path, relSlash, d) {
		return skipEntry(d)
	}
	w.dirsWithChildren[filepath.Dir(path)] = true
	w.files = append(w.files, w.newFileInfo(path, d, relPath))
	if d.IsDir() && w.noDescend[d.Name()] {
		return fs.SkipDir
	}
	return nil
}

// skipsEntry reports whether the entry is excluded, is the archive
// workspace root, or sits beyond the depth bound.
func (w *walker) skipsEntry(path, relSlash string, d fs.DirEntry) bool {
	if excluded(relSlash, w.excludes) {
		return true
	}
	// The archive workspace is pan-owned output; record its root but
	// never scan or clean inside it.
	if d.IsDir() && relSlash == w.archiveRel {
		return true
	}
	return pathDepth(path)-w.rootDepth > w.maxDepth
}

// skipEntry maps one skip decision onto walk control flow: directories
// prune the subtree, files are simply dropped.
func skipEntry(d fs.DirEntry) error {
	if d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// newFileInfo builds the inventory record for one entry.
func (w *walker) newFileInfo(path string, d fs.DirEntry, relPath string) FileInfo {
	fi := FileInfo{Path: path, RelPath: relPath, IsDir: d.IsDir()}
	if d.Type()&fs.ModeSymlink != 0 {
		fillSymlink(&fi, path)
	} else if info, infoErr := d.Info(); infoErr == nil {
		fi.Size = info.Size()
		fi.ModTime = info.ModTime()
		fi.Executable = info.Mode()&0o111 != 0
	}
	if fi.IsDir {
		w.dirs[path] = true
	}
	return fi
}

// fillSymlink records a symlink's own size, mtime, and link target without
// following it.
func fillSymlink(fi *FileInfo, path string) {
	fi.IsSymlink = true
	if lstat, lstatErr := os.Lstat(path); lstatErr == nil {
		fi.Size = lstat.Size()
		fi.ModTime = lstat.ModTime()
	}
	if target, readErr := os.Readlink(path); readErr == nil {
		fi.LinkTarget = target
	}
}

// finish post-processes the walked inventory: nested repositories are
// dropped, complete childless directories are flagged empty, and
// ignore-file suppression is applied.
func (w *walker) finish() []FileInfo {
	files := w.filterNestedRepos()
	w.markEmptyDirs()
	markSuppressed(files, loadIgnorePatterns(w.root, w.ignoreFile))
	return files
}

// filterNestedRepos drops every entry inside a discovered nested
// repository.
func (w *walker) filterNestedRepos() []FileInfo {
	if len(w.nestedRepos) == 0 {
		return w.files
	}
	filtered := w.files[:0]
	for _, f := range w.files {
		if isUnderAny(f.Path, w.nestedRepos) {
			continue
		}
		filtered = append(filtered, f)
	}
	return filtered
}

// markEmptyDirs flags directories the walk saw complete and childless; a
// directory is empty only when the walk saw all of its children, so
// directories at max depth may have unseen children below the bound.
func (w *walker) markEmptyDirs() {
	for i := range w.files {
		if w.isEmptyDirCandidate(&w.files[i]) {
			w.files[i].IsEmpty = true
		}
	}
}

func (w *walker) isEmptyDirCandidate(f *FileInfo) bool {
	if !f.IsDir || !w.dirs[f.Path] || w.dirsWithChildren[f.Path] {
		return false
	}
	return pathDepth(f.Path)-w.rootDepth < w.maxDepth
}

// pathDepth counts path separators in the cleaned path.
func pathDepth(p string) int {
	return strings.Count(filepath.Clean(p), string(os.PathSeparator))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// normalizeExcludes cleans exclusion entries the same way the config
// package does: repo-relative, slash-separated, sorted.
func normalizeExcludes(entries []string) []string {
	out := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		cleaned := strings.TrimPrefix(slashClean(entry), "/")
		if cleaned == "" || cleaned == "." {
			continue
		}
		if seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		out = append(out, cleaned)
	}
	sort.Strings(out)
	return out
}

func slashClean(p string) string {
	if p == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
}

// excluded reports whether rel (slash-separated, repo-relative) is equal to
// or beneath any exclusion entry.
func excluded(rel string, excludes []string) bool {
	for _, entry := range excludes {
		if rel == entry || strings.HasPrefix(rel, entry+"/") {
			return true
		}
	}
	return false
}

// loadIgnorePatterns reads the clean ignore file (default .pancleanignore)
// from the scan root. Format: one pattern per
// line; # comments and blank lines are skipped. Missing files mean no
// suppression.
func loadIgnorePatterns(root, name string) []string {
	if name == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// markSuppressed flags files matching ignore-file patterns.
func markSuppressed(files []FileInfo, patterns []string) {
	if len(patterns) == 0 {
		return
	}
	for i := range files {
		for _, pattern := range patterns {
			if matchesIgnorePattern(pattern, files[i].RelPath) {
				files[i].Suppressed = true
				break
			}
		}
	}
}

func matchesIgnorePattern(pattern, relPath string) bool {
	dirPattern := strings.HasSuffix(pattern, "/")
	pattern = filepath.Clean(pattern)
	relPath = filepath.Clean(relPath)
	if dirPattern {
		prefix := strings.TrimSuffix(pattern, string(os.PathSeparator))
		return relPath == prefix || strings.HasPrefix(relPath, prefix+string(os.PathSeparator))
	}
	if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
		return true
	}
	matched, err := filepath.Match(pattern, filepath.Base(relPath))
	return err == nil && matched
}

// isUnderAny reports whether path is inside any of the named directories.
func isUnderAny(path string, dirs map[string]bool) bool {
	for dir := range dirs {
		if strings.HasPrefix(path, dir+string(os.PathSeparator)) || path == dir {
			return true
		}
	}
	return false
}

// toSet converts a string slice to a lookup map.
func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}
