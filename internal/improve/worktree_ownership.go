package improve

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Ownership is kept beside the disposable tree, outside its Git index. The
// record survives a crash and identifies only this tree; it never authorizes
// pruning other linked worktrees or replaying an attempt.
type worktreeOwnership struct {
	Directory  string    `json:"directory"`
	Repository string    `json:"repository"`
	Baseline   string    `json:"baseline,omitempty"`
	Linked     bool      `json:"linked"`
	Created    time.Time `json:"created"`
}

func ownershipPath(dir string) string { return dir + ".pan-owner.json" }

func recordWorktreeOwnership(dir, repo, baseline string, linked bool) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	repository, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	body, err := json.Marshal(worktreeOwnership{Directory: absolute, Repository: repository, Baseline: baseline, Linked: linked, Created: time.Now().UTC()})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(ownershipPath(absolute), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("record disposable ownership: %w", err)
	}
	_, writeErr := file.Write(body)
	syncErr := file.Sync()
	return errors.Join(writeErr, syncErr, file.Close())
}

func requireOwnedWorktree(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	directoryInfo, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || filepath.Dir(absolute) == absolute {
		return errors.New("invalid disposable directory")
	}
	info, err := os.Lstat(ownershipPath(absolute))
	if err != nil {
		return fmt.Errorf("disposable ownership unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("disposable ownership is not regular")
	}
	data, err := os.ReadFile(ownershipPath(absolute))
	if err != nil {
		return err
	}
	var record worktreeOwnership
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	if record.Directory != absolute || record.Repository == "" {
		return errors.New("disposable ownership identity mismatch")
	}
	return nil
}

func removeOwnedCopy(dir string) error {
	if err := requireOwnedWorktree(dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return retainedWorktreeError(dir, err)
	}
	if err := os.Remove(ownershipPath(dir)); err != nil && !os.IsNotExist(err) {
		return retainedWorktreeError(dir, err)
	}
	return nil
}

func retainedWorktreeError(dir string, err error) error {
	return fmt.Errorf("disposable worktree retained at %q (ownership %q); inspect it before manually removing this exact worktree; do not prune or replay: %w", dir, ownershipPath(dir), err)
}

func uniqueAttemptBranch(prefix string) string { return fmt.Sprintf("%s-%s", prefix, rand.Text()) }
