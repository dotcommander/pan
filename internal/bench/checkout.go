package bench

// This file owns per-instance checkouts: work-directory lifecycle and the
// read-only `git archive` extraction with tar-safety guards.

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resetWorkDir removes one work directory and recreates it empty. The
// directory always sits directly under the run's work root and its name is
// sanitized, so removal stays contained.
func resetWorkDir(workDir string) error {
	if workDir == "" || workDir == "/" || workDir == "." || workDir == ".." {
		return errors.New("work directory is required")
	}
	parent := filepath.Dir(workDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("prepare work root: %w", err)
	}
	if err := os.RemoveAll(workDir); err != nil {
		return fmt.Errorf("reset work directory: %w", err)
	}
	return os.Mkdir(workDir, 0o755)
}

// extractArchive streams `git archive --format=tar <commit>` from the mirror
// into dst, extracting regular files and directories only. It reports
// whether the extraction was started (a started-but-failed extraction leaves
// partial content the caller should treat as a checkout failure, not an
// environment error) and any error. The mirror itself is only read.
func extractArchive(ctx context.Context, mirror, commit, dst string) (bool, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return false, errors.New("git is not installed")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", mirror, "archive", "--format=tar", commit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	reader := tar.NewReader(io.LimitReader(stdout, maxArchiveBytes))
	files := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return true, fmt.Errorf("read archive: %w", err)
		}
		target, safe := archiveTarget(dst, header.Name)
		if !safe {
			_ = cmd.Wait()
			return true, fmt.Errorf("archive entry %q escapes work directory", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				_ = cmd.Wait()
				return true, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				_ = cmd.Wait()
				return true, err
			}
			if err := writeBounded(target, reader, header.Size); err != nil {
				_ = cmd.Wait()
				return true, err
			}
			files++
		default:
			// Symlinks, submodules, and device entries are skipped; Pan
			// analysis ignores symlinks anyway.
		}
	}
	if err := cmd.Wait(); err != nil {
		return true, fmt.Errorf("git archive: %w", err)
	}
	if files == 0 {
		return true, errors.New("archive is empty")
	}
	return true, nil
}

// archiveTarget resolves one archive entry name under dst, rejecting
// absolute paths and any escape outside dst.
func archiveTarget(dst, name string) (string, bool) {
	name = filepath.ToSlash(name)
	if name == "" || strings.HasPrefix(name, "/") {
		return "", false
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return filepath.Join(dst, filepath.FromSlash(clean)), true
}

// writeBounded writes one extracted file, enforcing the header's declared
// size against the actual stream.
func writeBounded(target string, reader io.Reader, declared int64) error {
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, declared+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != declared {
		return fmt.Errorf("file %s: expected %d bytes, wrote %d", target, declared, written)
	}
	return nil
}
