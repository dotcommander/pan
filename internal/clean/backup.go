package clean

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// backupPrefix names pre-apply backups inside the archive directory.
const backupPrefix = "pre-cleanup-"

// CollectTargets returns the slash-relative paths an action list would
// touch: removal targets plus move sources and destinations. Git-only
// actions operate on tracked files that also appear as move/remove targets
// or exist on disk, so their paths are included too.
func CollectTargets(actions []Action) []string {
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, a := range actions {
		if a.Comment {
			continue
		}
		add(a.Source)
		add(a.Target)
	}
	return paths
}

// CreateBackup writes a tar.gz of every existing target under the archive
// directory and returns its slash-relative path plus the number of archived
// paths. When no target exists it returns ("", 0, nil). Directory targets
// are archived recursively; symlinks are stored with their link targets;
// every archived path must stay inside the scan root.
func CreateBackup(root, archiveDir string, targets []string, now time.Time) (string, int, error) {
	existing, err := existingTargets(root, targets)
	if err != nil {
		return "", 0, err
	}
	if len(existing) == 0 {
		return "", 0, nil
	}

	absArchive, err := prepareArchiveDir(root, archiveDir)
	if err != nil {
		return "", 0, err
	}
	ts := now.Format("20060102-150405")
	tarPath := uniquePath(filepath.Join(absArchive, backupPrefix+ts+".tar.gz"))
	relPath, err := writeBackupArchive(root, tarPath, existing)
	if err != nil {
		return "", 0, err
	}
	return relPath, len(existing), nil
}

// existingTargets filters the touch list down to paths that exist on disk,
// validating every path against the scan root first.
func existingTargets(root string, targets []string) ([]string, error) {
	var existing []string
	for _, target := range targets {
		abs, err := safeJoin(root, target)
		if err != nil {
			return nil, fmt.Errorf("backup target: %w", err)
		}
		if _, statErr := os.Lstat(abs); statErr == nil {
			existing = append(existing, target)
		}
	}
	return existing, nil
}

// prepareArchiveDir resolves and creates the archive directory, refusing a
// symlinked archive directory so a backup can never escape the root.
func prepareArchiveDir(root, archiveDir string) (string, error) {
	absArchive, err := safeJoin(root, archiveDir)
	if err != nil {
		return "", fmt.Errorf("backup directory: %w", err)
	}
	info, statErr := os.Lstat(absArchive)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("backup directory %q is a symlink", archiveDir)
		}
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect backup directory: %w", statErr)
	}
	if mkErr := os.MkdirAll(absArchive, dirPerm); mkErr != nil {
		return "", fmt.Errorf("create backup directory: %w", mkErr)
	}
	return absArchive, nil
}

// writeBackupArchive streams every existing target into one tar.gz at
// tarPath and returns its root-relative slash path.
func writeBackupArchive(root, tarPath string, existing []string) (string, error) {
	rootFS, rootErr := os.OpenRoot(root)
	if rootErr != nil {
		return "", fmt.Errorf("open scan root: %w", rootErr)
	}
	defer func() { _ = rootFS.Close() }()

	out, createErr := os.Create(tarPath)
	if createErr != nil {
		return "", fmt.Errorf("create backup: %w", createErr)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	writer := backupWriter{tw: tw, rootFS: rootFS, root: root}
	for _, name := range existing {
		if addErr := writer.addPath(name); addErr != nil {
			_ = tw.Close()
			_ = gz.Close()
			_ = out.Close()
			return "", addErr
		}
	}
	if closeErr := tw.Close(); closeErr != nil {
		return "", fmt.Errorf("finish backup tar: %w", closeErr)
	}
	if closeErr := gz.Close(); closeErr != nil {
		return "", fmt.Errorf("finish backup gzip: %w", closeErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		return "", fmt.Errorf("close backup: %w", closeErr)
	}
	rel, relErr := filepath.Rel(root, tarPath)
	if relErr != nil {
		return "", fmt.Errorf("resolve backup path: %w", relErr)
	}
	return filepath.ToSlash(rel), nil
}

// backupWriter bundles the archive writer with the root-scoped
// filesystem and scan root so the per-entry helpers stay within the
// argument limit.
type backupWriter struct {
	tw     *tar.Writer
	rootFS *os.Root
	root   string
}

// addPath archives one target path, recursing into directories.
func (w backupWriter) addPath(name string) error {
	full, err := safeJoin(w.root, name)
	if err != nil {
		return fmt.Errorf("backup target: %w", err)
	}
	return filepath.Walk(full, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("read backup path %s: %w", name, err)
		}
		return w.writeEntry(name, p, info)
	})
}

// writeEntry stores one walked path in the archive. Regular files
// are opened through the root-scoped filesystem so a raced or symlinked
// path can never pull outside-root content into the backup.
func (w backupWriter) writeEntry(name, p string, info os.FileInfo) error {
	rel, relErr := filepath.Rel(w.root, p)
	if relErr != nil || rel == parentDir || strings.HasPrefix(rel, parentDir+string(filepath.Separator)) {
		return fmt.Errorf("backup path %q is outside the scan root", name)
	}
	header, headerErr := tar.FileInfoHeader(info, "")
	if headerErr != nil {
		return fmt.Errorf("archive %s: %w", p, headerErr)
	}
	header.Name = filepath.ToSlash(rel)
	if info.Mode()&os.ModeSymlink != 0 {
		link, linkErr := os.Readlink(p)
		if linkErr != nil {
			return fmt.Errorf("read backup symlink %s: %w", p, linkErr)
		}
		header.Linkname = link
	}
	if writeErr := w.tw.WriteHeader(header); writeErr != nil {
		return fmt.Errorf("write backup header %s: %w", p, writeErr)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return w.copyFile(rel, p)
}

// copyFile streams one regular file into the archive.
func (w backupWriter) copyFile(rel, display string) error {
	f, openErr := w.rootFS.Open(rel)
	if openErr != nil {
		return fmt.Errorf("open backup file %s: %w", display, openErr)
	}
	_, copyErr := io.Copy(w.tw, f)
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("copy backup file %s: %w", display, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close backup file %s: %w", display, closeErr)
	}
	return nil
}

// uniquePath returns path when free, otherwise inserts -1, -2, ... before
// the extension until a free name is found.
func uniquePath(p string) string {
	if _, err := os.Lstat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return p
}
