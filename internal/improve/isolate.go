package improve

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxGitOutputBytes bounds every git plumbing read.
const maxGitOutputBytes = 1 << 20

// cmdGit is the constant git binary name; every argument arrives through
// cmd.Args so git always runs as one direct argv process, never through a
// shell.
const cmdGit = "git"

// dstDirPerm is the permission literal for destination directories pan
// creates; source-tree permissions are copied verbatim.
const dstDirPerm = 0o750

const gitDirName = ".git"

// WorkTree is the preflight verdict for a guarded improve run.
type WorkTree struct {
	// Inside is true when root resolves inside a git work tree.
	Inside bool
	// Clean is true when the work tree has no staged, unstaged, or
	// untracked changes.
	Clean bool
	// Head is the abbreviated commit id of HEAD, or "" when unavailable.
	Head string
	// Reason explains a refusal in one human sentence.
	Reason string
}

// OK reports whether a guarded run may proceed: an existing, clean work
// tree with a commit to snapshot.
func (w WorkTree) OK() bool {
	return w.Inside && w.Clean && w.Head != ""
}

// CheckWorkTree runs the guarded-run preflight. A target that is not a
// clean git work tree is a refusal, not an error: guarded improve runs
// snapshot the target head into their ledger and attribute breakage
// against a known baseline. Every invocation is a direct-argv git
// plumbing command with bounded output; no shell is involved.
func CheckWorkTree(ctx context.Context, root string) (WorkTree, error) {
	inside, err := gitOutput(ctx, root, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if isNotARepository(err) {
			return WorkTree{Reason: fmt.Sprintf("%s is not a git work tree", root)}, nil
		}
		return WorkTree{}, fmt.Errorf("probe work tree: %w", err)
	}
	if strings.TrimSpace(inside) != "true" {
		return WorkTree{Reason: fmt.Sprintf("%s is not a git work tree", root)}, nil
	}
	tree := WorkTree{Inside: true}

	status, err := gitOutput(ctx, root, "status", "--porcelain")
	if err != nil {
		return WorkTree{}, fmt.Errorf("read work tree status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		tree.Reason = "target working tree is not clean; commit or stash changes first"
		return tree, nil
	}
	tree.Clean = true

	head, err := gitOutput(ctx, root, "rev-parse", "--short", "HEAD")
	if err != nil {
		if isNoCommits(err) {
			tree.Reason = "target work tree has no commits to snapshot"
			return tree, nil
		}
		return WorkTree{}, fmt.Errorf("read HEAD: %w", err)
	}
	tree.Head = strings.TrimSpace(head)
	if tree.Head == "" {
		tree.Reason = "target work tree has no commits to snapshot"
		return tree, nil
	}
	return tree, nil
}

func isNotARepository(err error) bool {
	// gitOutput folds bounded stderr into the returned error text; the
	// exec.ExitError.Stderr field is always empty because output is
	// captured by a buffer, so match on the wrapped message.
	return strings.Contains(strings.ToLower(err.Error()), "not a git repository")
}

func isNoCommits(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown revision") || strings.Contains(msg, "bad revision") || strings.Contains(msg, "ambiguous argument")
}

// gitOutput runs one git plumbing command in dir with bounded output.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, cmdGit)
	cmd.Dir = dir
	cmd.Args = append(cmd.Args, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() > maxGitOutputBytes {
		return "", fmt.Errorf("git %s: output exceeds %d bytes", args[0], maxGitOutputBytes)
	}
	return stdout.String(), nil
}

// CopyTree copies src's work tree into dst, which must not exist. The
// .git directory is skipped: the isolated copy exists only to run the Go
// toolchain against identical sources, never to mutate repository state.
// Symlinks are replicated as symlinks; other irregular files are skipped.
// Both sides run through os.Root so every read and write is confined to
// its tree by construction.
func CopyTree(dst, src string) error {
	srcRoot, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	srcFS, err := os.OpenRoot(srcRoot)
	if err != nil {
		return fmt.Errorf("open source root: %w", err)
	}
	defer func() { _ = srcFS.Close() }()
	if mkdirErr := os.MkdirAll(dst, dstDirPerm); mkdirErr != nil {
		return fmt.Errorf("create destination: %w", mkdirErr)
	}
	dstFS, err := os.OpenRoot(dst)
	if err != nil {
		return fmt.Errorf("open destination root: %w", err)
	}
	defer func() { _ = dstFS.Close() }()
	copier := &treeCopier{src: srcFS, dst: dstFS}
	// Walking the rooted FS yields slash-relative names that are valid for
	// both Root handles without any path re-joining.
	return fs.WalkDir(srcFS.FS(), ".", copier.visit)
}

// treeCopier copies one rooted source tree into a rooted destination tree.
type treeCopier struct {
	src *os.Root
	dst *os.Root
}

// visit copies one walked entry. The top-level .git directory is the only
// skipped subtree.
func (c *treeCopier) visit(name string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if name == "." {
		return nil
	}
	if d.IsDir() {
		// Walk names are root-relative, so this matches only the
		// repository's own .git, never a nested same-named directory.
		if name == gitDirName {
			return fs.SkipDir
		}
		return c.copyDir(name, d)
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return c.copySymlink(name)
	}
	if !d.Type().IsRegular() {
		return nil
	}
	return c.copyFile(name, d)
}

// copyDir creates the destination directory with the source's mode.
func (c *treeCopier) copyDir(name string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	return c.dst.MkdirAll(name, info.Mode().Perm())
}

// copySymlink replicates one symlink as a symlink; its target is data,
// never a path pan dereferences.
func (c *treeCopier) copySymlink(name string) error {
	link, err := c.src.Readlink(name)
	if err != nil {
		return err
	}
	return c.dst.Symlink(link, name)
}

// copyFile copies one regular file with its mode. Parent directories were
// created by their own walk visits, so no per-file mkdir is needed.
func (c *treeCopier) copyFile(name string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	data, err := c.src.ReadFile(name)
	if err != nil {
		return err
	}
	return c.dst.WriteFile(name, data, info.Mode().Perm())
}

// IsolateWorkTree copies root's work tree into a fresh temporary
// directory and returns it. The caller owns the copy and must remove it.
func IsolateWorkTree(root string) (string, error) {
	dir, err := os.MkdirTemp("", "pan-improve-*")
	if err != nil {
		return "", fmt.Errorf("create isolated copy: %w", err)
	}
	if err := CopyTree(dir, root); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("copy work tree: %w", err)
	}
	return dir, nil
}
