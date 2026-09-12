package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (w *releaseWorkflow) inspectTap(ctx context.Context, fetch bool) (string, error) {
	tapRoot, err := w.proc.capture(ctx, w.root, map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1"},
		"brew", "--repository", tapName)
	if err != nil {
		return "", err
	}
	if err := w.requireCleanRepository(ctx, tapRoot); err != nil {
		return "", err
	}
	branch, err := w.proc.capture(ctx, tapRoot, nil, "git", "branch", "--show-current")
	if err != nil {
		return "", err
	}
	if branch != "main" {
		return "", fmt.Errorf("tap branch is %q, want main", branch)
	}
	remote, err := w.proc.capture(ctx, tapRoot, nil, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	repository, err := githubRepository(remote)
	if err != nil {
		return "", err
	}
	if repository != tapRepository {
		return "", fmt.Errorf("tap origin is %s, want %s", repository, tapRepository)
	}
	if fetch {
		if err := w.proc.stream(ctx, tapRoot, nil, "git", releaseFetchArgs()...); err != nil {
			return "", err
		}
		head, err := w.proc.capture(ctx, tapRoot, nil, "git", "rev-parse", "HEAD")
		if err != nil {
			return "", err
		}
		remoteHead, err := w.proc.capture(ctx, tapRoot, nil, "git", "rev-parse", "origin/main")
		if err != nil {
			return "", err
		}
		if head != remoteHead {
			return "", errors.New("tap main is not synchronized with origin/main")
		}
	} else if _, err := w.proc.capture(ctx, tapRoot, nil, "git", "ls-remote", "--exit-code", "origin", "refs/heads/main"); err != nil {
		return "", err
	}
	return tapRoot, nil
}

func (w *releaseWorkflow) tapPreflight(ctx context.Context) error {
	tapRoot, err := w.inspectTap(ctx, true)
	if err != nil {
		return err
	}
	remoteBase, err := w.proc.capture(ctx, tapRoot, nil, "git", "rev-parse", "origin/main")
	if err != nil {
		return err
	}
	w.state.TapRoot = tapRoot
	w.state.TapRemoteBase = remoteBase
	return nil
}

func (w *releaseWorkflow) tapPrepare(context.Context) error {
	formulaPath := filepath.Join(w.state.TapRoot, "Formula", "pan.rb")
	content, err := os.ReadFile(formulaPath)
	if os.IsNotExist(err) {
		content, err = os.ReadFile(filepath.Join(w.root, "Formula", "pan.rb"))
	}
	if err != nil {
		return err
	}
	updated, err := updateFormula(content, w.archiveURL(), w.state.ArchiveSHA256)
	if err != nil {
		return err
	}
	if string(updated) == string(content) {
		return nil
	}
	return writeFileAtomic(formulaPath, updated, 0o644)
}

func (w *releaseWorkflow) tapVerify(ctx context.Context) error {
	formulaPath := filepath.Join(w.state.TapRoot, "Formula", "pan.rb")
	unstaged, err := w.proc.capture(ctx, w.state.TapRoot, nil, "git", "diff", "--name-only", "-z")
	if err != nil {
		return err
	}
	untracked, err := w.proc.capture(ctx, w.state.TapRoot, nil, "git", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	paths := append(nulPaths(unstaged), nulPaths(untracked)...)
	if !samePaths(paths, "Formula/pan.rb") {
		return fmt.Errorf("tap preparation changed unexpected paths: %q", paths)
	}
	env := map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1"}
	if err := w.proc.stream(ctx, w.state.TapRoot, env, "brew", "style", formulaPath); err != nil {
		return err
	}
	if err := w.proc.stream(ctx, w.state.TapRoot, env, "brew", "audit", "--strict", "--online", formulaName); err != nil {
		return err
	}
	return nil
}

func (w *releaseWorkflow) tapCommit(ctx context.Context) error {
	clean, err := w.repositoryClean(ctx, w.state.TapRoot)
	if err != nil {
		return err
	}
	if clean {
		content, err := w.proc.capture(ctx, w.state.TapRoot, nil, "git", "show", "HEAD:Formula/pan.rb")
		if err != nil || !strings.Contains(content, w.archiveURL()) || !strings.Contains(content, w.state.ArchiveSHA256) {
			return errors.New("tap is clean but the target formula commit is not present")
		}
		commit, err := w.proc.capture(ctx, w.state.TapRoot, nil, "git", "rev-parse", "HEAD")
		w.state.TapCommit = commit
		return err
	}
	if err := w.proc.stream(ctx, w.state.TapRoot, nil, "git", "--literal-pathspecs", "add", "--", "Formula/pan.rb"); err != nil {
		return err
	}
	if err := w.proc.stream(ctx, w.state.TapRoot, nil, "git", "diff", "--cached", "--check"); err != nil {
		return err
	}
	if err := w.proc.stream(ctx, w.state.TapRoot, nil, "git", "diff", "--cached"); err != nil {
		return err
	}
	if err := w.proc.stream(ctx, w.state.TapRoot, nil, "git", "commit", "-m", "chore(pan): update formula to "+w.version,
		"-m", "Point the formula at the published source archive and verified checksum."); err != nil {
		return err
	}
	commit, err := w.proc.capture(ctx, w.state.TapRoot, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	w.state.TapCommit = commit
	return w.requireCleanRepository(ctx, w.state.TapRoot)
}

func (w *releaseWorkflow) tapPublish(ctx context.Context) error {
	remoteHead, err := w.proc.capture(ctx, w.state.TapRoot, nil, "git", "ls-remote", "origin", "refs/heads/main")
	if err != nil {
		return err
	}
	remoteOID := strings.Fields(remoteHead)
	if len(remoteOID) == 2 && remoteOID[0] == w.state.TapCommit {
		return nil
	}
	if len(remoteOID) != 2 {
		return errors.New("tap remote main is missing")
	}
	if remoteOID[0] != w.state.TapRemoteBase {
		return fmt.Errorf("tap origin/main drifted from %s to %s", shortOID(w.state.TapRemoteBase), shortOID(remoteOID[0]))
	}
	if err := w.proc.stream(ctx, w.state.TapRoot, nil, "git", "push", "origin", "main"); err != nil {
		return err
	}
	remoteHead, err = w.proc.capture(ctx, w.state.TapRoot, nil, "git", "ls-remote", "origin", "refs/heads/main")
	if err != nil {
		return err
	}
	remoteOID = strings.Fields(remoteHead)
	if len(remoteOID) != 2 || remoteOID[0] != w.state.TapCommit {
		return errors.New("remote tap main does not match the formula commit")
	}
	return nil
}

func (w *releaseWorkflow) finalReadback(ctx context.Context) error {
	head, tag, err := w.sourceRemoteState(ctx)
	if err != nil {
		return err
	}
	if head != w.state.ReleaseCommit || tag != w.state.ReleaseCommit {
		return errors.New("source remote readback does not match the release commit")
	}
	if err := w.verifyGitHubRelease(ctx); err != nil {
		return err
	}
	info, err := w.proc.capture(ctx, w.state.TapRoot, map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1"},
		"brew", "info", formulaName, "--json=v2")
	if err != nil {
		return err
	}
	var payload struct {
		Formulae []struct {
			Versions struct {
				Stable string `json:"stable"`
			} `json:"versions"`
			URLs struct {
				Stable struct {
					URL      string `json:"url"`
					Checksum string `json:"checksum"`
				} `json:"stable"`
			} `json:"urls"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal([]byte(info), &payload); err != nil {
		return err
	}
	if len(payload.Formulae) != 1 || payload.Formulae[0].Versions.Stable != w.version ||
		payload.Formulae[0].URLs.Stable.URL != w.archiveURL() ||
		payload.Formulae[0].URLs.Stable.Checksum != w.state.ArchiveSHA256 {
		return errors.New("Homebrew formula readback does not match the release")
	}
	if err := w.requireCleanRepository(ctx, w.root); err != nil {
		return err
	}
	return w.requireCleanRepository(ctx, w.state.TapRoot)
}
