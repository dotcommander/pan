package clean

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// symlinkedArchiveRoot seeds a scan root whose configured archive directory
// is a symlink pointing at an outside directory, plus one touched file. It
// returns the root and the outside directory.
func symlinkedArchiveRoot(t *testing.T) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	outside = t.TempDir()
	writeTestFile(t, root, "source.txt", "source")
	if err := os.MkdirAll(filepath.Join(root, ".work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".work", "archive")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

// archiveBackups lists pre-cleanup archives written under dir.
func archiveBackups(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "pre-cleanup-*.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// TestCreateBackupRejectsSymlinkedArchiveDir pins the backup gate that
// refuses a symlinked archive directory: no archive is written into the
// symlink target and no touched path changes.
func TestCreateBackupRejectsSymlinkedArchiveDir(t *testing.T) {
	t.Parallel()
	root, outside := symlinkedArchiveRoot(t)

	if _, count, err := CreateBackup(root, ".work/archive", []string{"source.txt"}, applyNow()); err == nil || !strings.Contains(err.Error(), "symlink") || count != 0 {
		t.Fatalf("CreateBackup = (%d, %v), want symlinked archive rejection", count, err)
	}
	if matches := archiveBackups(t, outside); len(matches) != 0 {
		t.Fatalf("symlinked archive directory wrote backups into its target: %v", matches)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "source.txt")); statErr != nil {
		t.Fatalf("touched path changed during rejected backup: %v", statErr)
	}
}

// TestApplyRefusesSymlinkedArchiveDir proves the apply-level contract for
// the same gate: when the backup cannot be created, nothing at all is
// changed — no move executes, no manifest is written, and the symlink
// target stays empty.
func TestApplyRefusesSymlinkedArchiveDir(t *testing.T) {
	t.Parallel()
	root, outside := symlinkedArchiveRoot(t)

	result, err := Apply(context.Background(), testOptions(root), []Action{{
		Category: "archive", Kind: KindMove, Source: "source.txt", Target: "moved.txt",
	}}, true)
	if err == nil {
		t.Fatal("apply accepted a symlinked archive directory")
	}
	if result.Backup != "" || result.Manifest != "" || result.BackedUp != 0 || result.Counts.Executed != 0 {
		t.Fatalf("apply recorded progress despite backup refusal: %+v", result)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "source.txt")); statErr != nil {
		t.Fatalf("apply mutated despite backup refusal: %v", statErr)
	}
	if matches := archiveBackups(t, outside); len(matches) != 0 {
		t.Fatalf("symlinked archive directory wrote backups into its target: %v", matches)
	}
}

// TestApplyNeverFollowsFinalSymlinks pins that remove and move act on a
// final-component symlink itself: the link disappears or moves as a link,
// and its target is never read, moved, or deleted.
func TestApplyNeverFollowsFinalSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "doomed-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "moved-link")); err != nil {
		t.Fatal(err)
	}

	result, err := Apply(context.Background(), testOptions(root), []Action{
		{Category: "test", Kind: KindRemove, Target: "doomed-link"},
		{Category: "test", Kind: KindMove, Source: "moved-link", Target: "archive/moved-link"},
	}, true)
	if err != nil {
		t.Fatalf("apply over final symlinks failed: %v", err)
	}
	if result.Counts.Executed != 2 || result.Counts.Failed != 0 {
		t.Fatalf("counts = %+v, want both actions executed", result.Counts)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "doomed-link")); !os.IsNotExist(statErr) {
		t.Fatalf("removed link still on disk: %v", statErr)
	}
	moved := filepath.Join(root, "archive", "moved-link")
	info, statErr := os.Lstat(moved)
	if statErr != nil {
		t.Fatalf("moved link missing: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("moved path is not a symlink; apply dereferenced the link")
	}
	if link, err := os.Readlink(moved); err != nil || link != secret {
		t.Fatalf("moved link target = (%q, %v), want %q", link, err, secret)
	}
	if contents, err := os.ReadFile(secret); err != nil || string(contents) != "secret" {
		t.Fatalf("symlink target changed: %q, %v", contents, err)
	}
}

// TestBackupArchivesSymlinkedChildrenWithoutFollowing pins the no-follow
// invariant inside backup trees: symlinked files and directories inside a
// backed-up directory are stored as links, so outside-root content can
// never enter the archive through them.
func TestBackupArchivesSymlinkedChildrenWithoutFollowing(t *testing.T) {
	t.Parallel()
	root, secretFile, outsideDir := seedSymlinkedBackupTree(t)

	rel, count, err := CreateBackup(root, ".work/archive", []string{"data"}, applyNow())
	if err != nil {
		t.Fatalf("backup over symlinked children failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("backed up %d paths, want 1", count)
	}
	headers := readBackupHeaders(t, filepath.Join(root, filepath.FromSlash(rel)))

	if header := headers["data/real.txt"]; header == nil || header.Typeflag != tar.TypeReg {
		t.Fatalf("data/real.txt header = %+v, want a regular file", headers["data/real.txt"])
	}
	assertArchivedSymlink(t, headers, "data/secret-link", secretFile)
	assertArchivedSymlink(t, headers, "data/outside-dir-link", outsideDir)
	assertNoOutsideRootEntries(t, headers)
}

// seedSymlinkedBackupTree creates data/real.txt plus file and directory
// symlinks pointing outside the scan root, with outside-root content a
// dereferencing backup would swallow.
func seedSymlinkedBackupTree(t *testing.T) (root, secretFile, outsideDir string) {
	t.Helper()
	root = t.TempDir()
	outsideDir = t.TempDir()
	secretFile = filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outsideDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "sub", "deep.txt"), []byte("deep"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "data/real.txt", "real")
	if err := os.Symlink(secretFile, filepath.Join(root, "data", "secret-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "data", "outside-dir-link")); err != nil {
		t.Fatal(err)
	}
	return root, secretFile, outsideDir
}

// assertArchivedSymlink pins that name is archived as a symlink pointing
// at target.
func assertArchivedSymlink(t *testing.T, headers map[string]*tar.Header, name, target string) {
	t.Helper()
	header, ok := headers[name]
	if !ok {
		t.Fatalf("backup entries = %v, want %q archived as a link", headerNames(headers), name)
	}
	if header.Typeflag != tar.TypeSymlink {
		t.Fatalf("%s archived as type %q, want a symlink", name, string(header.Typeflag))
	}
	if header.Linkname != target {
		t.Fatalf("%s link target = %q, want %q", name, header.Linkname, target)
	}
}

// assertNoOutsideRootEntries pins that no archived name carries content
// that only exists through a symlink target.
func assertNoOutsideRootEntries(t *testing.T, headers map[string]*tar.Header) {
	t.Helper()
	for name := range headers {
		if strings.Contains(name, "deep.txt") || strings.Contains(name, "sub/") || strings.Contains(name, "secret.txt") {
			t.Fatalf("backup dereferenced a symlink and archived outside-root path %q", name)
		}
	}
}

// TestApplyRefusesUnsafeActionPaths is defense in depth: even though git
// actions run direct argv and remove/move validate at execution time, a
// hand-crafted action touching .git or escaping the scan root is refused
// at the backup stage, so nothing is mutated at all.
func TestApplyRefusesUnsafeActionPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, root, ".git/config", "[core]\n")
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	unsafe := []Action{
		{Category: "test", Kind: KindRemove, Target: ".git/config"},
		{Category: "test", Kind: KindMove, Source: ".git/config", Target: "stolen-config"},
		{Category: "test", Kind: KindRemove, Target: "../outside.txt"},
		{Category: "test", Kind: KindMove, Source: "../outside.txt", Target: "stolen-outside"},
	}
	for _, action := range unsafe {
		result, err := Apply(context.Background(), testOptions(root), []Action{action}, true)
		if err == nil {
			t.Fatalf("apply accepted unsafe action %+v", action)
		}
		if !strings.Contains(err.Error(), "backup failed, nothing was changed") {
			t.Fatalf("apply error = %v, want backup-stage refusal", err)
		}
		if result.Backup != "" || result.Manifest != "" || result.Counts.Executed != 0 {
			t.Fatalf("unsafe action %+v recorded progress: %+v", action, result)
		}
		if _, statErr := os.Lstat(filepath.Join(root, ".git", "config")); statErr != nil {
			t.Fatalf("unsafe action %+v removed .git/config: %v", action, statErr)
		}
		if _, statErr := os.Lstat(filepath.Join(outside, "outside.txt")); statErr != nil {
			t.Fatalf("unsafe action %+v touched outside-root content: %v", action, statErr)
		}
	}
}

// readBackupHeaders returns every archive entry header keyed by archive
// path, including non-regular entries such as symlinks.
func readBackupHeaders(t *testing.T, path string) map[string]*tar.Header {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gz.Close() })
	tr := tar.NewReader(gz)
	headers := map[string]*tar.Header{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return headers
		}
		if err != nil {
			t.Fatal(err)
		}
		headers[header.Name] = header
	}
}

// headerNames lists the archive entry names for failure messages.
func headerNames(headers map[string]*tar.Header) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	return names
}
