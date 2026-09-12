package clean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

// Action status labels shared by results and the manifest.
const (
	statusPlanned = "planned"
	statusOK      = "ok"
	statusFailed  = "failed"
	statusSkipped = "skipped"
	statusNotRun  = "not_run"
)

// Permission bits for created directories and written files.
const (
	dirPerm  os.FileMode = 0o750
	filePerm os.FileMode = 0o600
)

// ApplyCounts summarizes one apply run.
type ApplyCounts struct {
	Executed int `json:"executed"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
	NotRun   int `json:"not_run"`
}

// ActionResult records the outcome of one planned action.
type ActionResult struct {
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Display  string `json:"display"`
	Status   string `json:"status"` // planned | ok | failed | skipped | not_run
	Source   string `json:"source,omitempty"`
	Target   string `json:"target,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ApplyResult is the `clean apply` result for both dry-runs and confirmed
// runs. A confirmed run additionally records the backup and manifest paths.
type ApplyResult struct {
	Schema    string         `json:"schema"`
	Path      string         `json:"path"`
	Confirmed bool           `json:"confirmed"`
	Backup    string         `json:"backup,omitempty"`
	Manifest  string         `json:"manifest,omitempty"`
	BackedUp  int            `json:"backed_up"`
	Counts    ApplyCounts    `json:"counts"`
	Actions   []ActionResult `json:"actions,omitempty"`
	Note      string         `json:"note,omitempty"`
}

// ManifestAction is one executed (or failed) action in the manifest.
type ManifestAction struct {
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target,omitempty"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// Manifest is the durable record written beside the backup after a
// confirmed apply: what was backed up, what ran, and what each action did.
type Manifest struct {
	Schema     string           `json:"schema"`
	Created    time.Time        `json:"created"`
	Repository string           `json:"repository"`
	Backup     string           `json:"backup,omitempty"`
	BackedUp   int              `json:"backed_up"`
	Actions    []ManifestAction `json:"actions"`
	Summary    ApplyCounts      `json:"summary"`
}

// Apply executes the action list. Without confirm it is a pure dry-run:
// nothing is written, no backup is created, and every actionable step is
// reported as planned. With confirm it backs up every existing touched path
// into a tar.gz archive, executes the actions in order (stopping at the
// first failure), and writes the JSON manifest beside the backup.
func Apply(ctx context.Context, opts Options, actions []Action, confirm bool) (ApplyResult, error) {
	opts, err := opts.Resolved()
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{Schema: ApplySchema, Path: opts.Root, Confirmed: confirm}
	if len(actions) == 0 {
		result.Note = "nothing to clean up"
		return result, nil
	}
	if !confirm {
		planDryRun(&result, actions)
		return result, nil
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	now := opts.now()
	targets := CollectTargets(actions)
	backup, backedUp, err := CreateBackup(opts.Root, slashClean(opts.Rules.ArchiveDir), targets, now)
	if err != nil {
		return result, fmt.Errorf("backup failed, nothing was changed: %w", err)
	}
	result.Backup, result.BackedUp = backup, backedUp
	cancelErr := executeAll(ctx, &result, opts.Root, actions)

	manifestPath, err := writeManifest(opts, now, backup, backedUp, result)
	if err != nil {
		return result, fmt.Errorf("write manifest: %w", err)
	}
	result.Manifest = manifestPath
	if cancelErr != nil {
		return result, cancelErr
	}
	if result.Counts.Failed > 0 {
		return result, fmt.Errorf("%d cleanup action(s) failed; remaining actions were not run", result.Counts.Failed)
	}
	return result, nil
}

// planDryRun records every action as planned (or skipped for manual-review
// comments) without touching anything.
func planDryRun(result *ApplyResult, actions []Action) {
	for _, a := range actions {
		status := statusPlanned
		if a.Comment {
			status = statusSkipped
			result.Counts.Skipped++
		}
		result.Actions = append(result.Actions, ActionResult{
			Category: a.Category, Kind: string(a.Kind), Display: a.Display,
			Status: status, Source: a.Source, Target: a.Target,
		})
	}
	result.Note = "dry-run: re-run with --confirm to back up and apply"
}

// executeAll runs the confirmed action sequence in order, stopping at the
// first failure or context cancellation, and recording every outcome.
func executeAll(ctx context.Context, result *ApplyResult, root string, actions []Action) error {
	stopped := false
	var cancelErr error
	for _, a := range actions {
		entry := ActionResult{Category: a.Category, Kind: string(a.Kind), Display: a.Display, Source: a.Source, Target: a.Target}
		switch {
		case stopped:
			markNotRun(&entry, &result.Counts, a)
		case a.Comment:
			entry.Status = statusSkipped
			result.Counts.Skipped++
		default:
			if err := ctx.Err(); err != nil {
				stopped = true
				cancelErr = err
				markNotRun(&entry, &result.Counts, a)
				entry.Error = err.Error()
				break
			}
			stopped = executeOne(ctx, root, a, &entry, &result.Counts)
		}
		result.Actions = append(result.Actions, entry)
	}
	return cancelErr
}

// markNotRun records a post-failure action: manual-review comments stay
// skipped, every other action is recorded not_run.
func markNotRun(entry *ActionResult, counts *ApplyCounts, a Action) {
	if a.Comment {
		entry.Status = statusSkipped
		counts.Skipped++
		return
	}
	entry.Status = statusNotRun
	counts.NotRun++
}

// executeOne runs one actionable step and records its outcome; it reports
// whether execution must stop.
func executeOne(ctx context.Context, root string, a Action, entry *ActionResult, counts *ApplyCounts) bool {
	target, moved, err := runAction(ctx, root, a)
	if err != nil {
		entry.Status, entry.Error = statusFailed, err.Error()
		counts.Failed++
		return true
	}
	entry.Status = statusOK
	if moved {
		entry.Target = target
	}
	counts.Executed++
	return false
}

// runAction executes one non-comment action. Moves run through executeMove
// and report their actual destination; every other kind runs through
// executeAction.
func runAction(ctx context.Context, root string, a Action) (target string, moved bool, err error) {
	if a.Kind == KindMove {
		dst, moveErr := executeMove(root, a)
		return dst, true, moveErr
	}
	return "", false, executeAction(ctx, root, a)
}

// executeAction runs mkdir, remove, and git actions. Every path is
// validated against the scan root before anything is touched; git runs as
// one direct argv command, never through a shell.
func executeAction(ctx context.Context, root string, a Action) error {
	switch a.Kind {
	case KindMkdir:
		dir, err := safeJoin(root, a.Target)
		if err != nil {
			return err
		}
		if mkErr := os.MkdirAll(dir, dirPerm); mkErr != nil {
			return fmt.Errorf("create directory: %w", mkErr)
		}
		return nil
	case KindRemove:
		target, err := safeJoin(root, a.Target)
		if err != nil {
			return err
		}
		if _, statErr := os.Lstat(target); statErr != nil {
			return fmt.Errorf("remove %s: %w", a.Target, statErr)
		}
		if rmErr := os.RemoveAll(target); rmErr != nil {
			return fmt.Errorf("remove %s: %w", a.Target, rmErr)
		}
		return nil
	case KindMove:
		// Moves execute through executeMove, which resolves and reports
		// the actual destination; routing one here would lose that result.
		return errors.New("move actions execute through executeMove")
	case KindGit:
		if len(a.Argv) < 2 {
			return errors.New("git action has no arguments")
		}
		if _, gitErr := gitRun(ctx, root, a.Argv[1:]...); gitErr != nil {
			return gitErr
		}
		return nil
	case KindReview:
		return errors.New("review actions are never executed")
	default:
		return fmt.Errorf("unsupported action kind %q", a.Kind)
	}
}

// executeMove moves Source to Target on the filesystem. Destination
// directories are created, and an existing destination is never
// overwritten: the target is suffixed -1, -2, ... until free. The actual
// destination is returned.
func executeMove(root string, a Action) (string, error) {
	src, err := safeJoin(root, a.Source)
	if err != nil {
		return "", err
	}
	dst, err := safeJoin(root, a.Target)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Lstat(src); statErr != nil {
		return "", fmt.Errorf("move %s: %w", a.Source, statErr)
	}
	if mkErr := os.MkdirAll(filepath.Dir(dst), dirPerm); mkErr != nil {
		return "", fmt.Errorf("create destination directory: %w", mkErr)
	}
	dst = uniquePath(dst)
	if renameErr := os.Rename(src, dst); renameErr != nil {
		return "", fmt.Errorf("move %s: %w", a.Source, renameErr)
	}
	if rel, relErr := filepath.Rel(root, dst); relErr == nil {
		return filepath.ToSlash(rel), nil
	}
	return filepath.ToSlash(dst), nil
}

// writeManifest persists the durable apply record beside the backup and
// returns its slash-relative path.
func writeManifest(opts Options, now time.Time, backup string, backedUp int, result ApplyResult) (string, error) {
	manifest := Manifest{
		Schema: ManifestSchema, Created: now, Repository: result.Path,
		Backup: backup, BackedUp: backedUp, Summary: result.Counts,
	}
	for _, a := range result.Actions {
		manifest.Actions = append(manifest.Actions, ManifestAction{
			Category: a.Category, Kind: a.Kind, Source: a.Source,
			Target: a.Target, Status: a.Status, Error: a.Error,
		})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	var manifestRel string
	if backup != "" && strings.HasSuffix(backup, ".tar.gz") {
		stem := strings.TrimSuffix(path.Base(backup), ".tar.gz")
		manifestRel = path.Join(slashClean(opts.Rules.ArchiveDir), stem+".manifest.json")
	} else {
		manifestRel = path.Join(slashClean(opts.Rules.ArchiveDir), backupPrefix+now.Format("20060102-150405")+".manifest.json")
	}
	absManifest, err := safeJoin(opts.Root, manifestRel)
	if err != nil {
		return "", fmt.Errorf("manifest path: %w", err)
	}
	absManifest = uniquePath(absManifest)
	if writeErr := atomicfile.Write(absManifest, append(data, '\n'), filePerm); writeErr != nil {
		return "", fmt.Errorf("write manifest: %w", writeErr)
	}
	if rel, relErr := filepath.Rel(opts.Root, absManifest); relErr == nil {
		return filepath.ToSlash(rel), nil
	}
	return "", nil
}

// safeJoin resolves a slash-relative action path under root, rejecting
// empty, absolute, parent-escaping, and .git-touching paths. This is the
// single path-safety gate every mutation passes through.
func safeJoin(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
	}
	cleaned := path.Clean(rel)
	if path.IsAbs(cleaned) || cleaned == parentDir || strings.HasPrefix(cleaned, parentDir+"/") {
		return "", fmt.Errorf("path %q escapes the scan root", rel)
	}
	parts := strings.Split(filepath.ToSlash(cleaned), "/")
	current := root
	for i, component := range parts {
		if component == gitDirName {
			return "", fmt.Errorf("path %q touches the git directory", rel)
		}
		current = filepath.Join(current, filepath.FromSlash(component))
		if i == len(parts)-1 {
			continue
		}
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path %q traverses symlinked directory %q", rel, component)
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect path %q: %w", rel, statErr)
		}
	}
	return current, nil
}
