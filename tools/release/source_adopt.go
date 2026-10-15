package main

import (
	"context"
	"errors"
	"fmt"
)

// Adopt phases validate a release whose commit, tag, push, and GitHub
// release were already published outside this tool. They never create
// commits, releases, or pushes; the only local write is materializing
// the published tag for validation when fetch tag-following has not
// already done so. Every required state must already hold remotely so
// the archive and Homebrew tap phases can proceed from verified state.

// adoptSourcePreflight verifies the required tools and the externally
// published source state, then records the release identity.
func (w *releaseWorkflow) adoptSourcePreflight(ctx context.Context) error {
	if err := requireTools("git", "gh", "brew", "just"); err != nil {
		return err
	}
	if err := w.adoptSourceState(ctx); err != nil {
		return err
	}
	return w.proc.stream(ctx, w.root, nil, "gh", "auth", "status")
}

// adoptSourceState requires a clean main worktree whose local tag, remote
// tag, and origin/main all identify one release commit that carries the
// target binaryVersion.
func (w *releaseWorkflow) adoptSourceState(ctx context.Context) error {
	if err := w.requireCleanRepository(ctx, w.root); err != nil {
		return err
	}
	if _, err := w.requireSourceIdentity(ctx); err != nil {
		return err
	}
	if err := w.proc.stream(ctx, w.root, nil, "git", releaseFetchArgs()...); err != nil {
		return err
	}
	localTag, err := w.proc.capture(ctx, w.root, nil, "git", "tag", "--list", w.tag)
	if err != nil {
		return err
	}
	if localTag == "" {
		// Materialize the published tag locally; an exact refspec avoids
		// depending on fetch tag auto-following.
		if err := w.proc.stream(ctx, w.root, nil, "git", "fetch", "--no-tags", "origin",
			"refs/tags/"+w.tag+":refs/tags/"+w.tag); err != nil {
			return err
		}
		localTag, err = w.proc.capture(ctx, w.root, nil, "git", "tag", "--list", w.tag)
		if err != nil {
			return err
		}
		if localTag == "" {
			return fmt.Errorf("adopt requires local tag %s to exist", w.tag)
		}
	}
	releaseCommit, err := w.proc.capture(ctx, w.root, nil, "git", "rev-parse", w.tag+"^{}")
	if err != nil {
		return err
	}
	if err := w.validateAdoptedReleaseCommit(ctx, releaseCommit); err != nil {
		return err
	}
	remoteHead, remoteTag, err := w.sourceRemoteState(ctx)
	if err != nil {
		return err
	}
	if remoteTag != releaseCommit {
		return fmt.Errorf("remote tag %s does not point at the release commit %s", w.tag, shortOID(releaseCommit))
	}
	if remoteHead != releaseCommit {
		return fmt.Errorf("origin/main is %s, want release commit %s; final readback requires origin/main at the release commit", shortOID(remoteHead), shortOID(releaseCommit))
	}
	w.state.ReleaseCommit = releaseCommit
	w.state.SourceStart = releaseCommit
	w.state.SourceRemoteBase = remoteHead
	if previous, err := w.commitBinaryVersion(ctx, releaseCommit+"^"); err == nil {
		w.state.PreviousVersion = previous
	}
	return nil
}

// adoptSourcePrepare is a no-op: the adopted release commit already
// carries the target version.
func (w *releaseWorkflow) adoptSourcePrepare(context.Context) error { return nil }

// adoptSourceCommit re-asserts the clean worktree at commit time.
func (w *releaseWorkflow) adoptSourceCommit(ctx context.Context) error {
	return w.requireCleanRepository(ctx, w.root)
}

// adoptSourceTag validates that the existing tag targets the release
// commit instead of creating one.
func (w *releaseWorkflow) adoptSourceTag(ctx context.Context) error {
	target, err := w.proc.capture(ctx, w.root, nil, "git", "rev-parse", w.tag+"^{}")
	if err != nil {
		return err
	}
	if target != w.state.ReleaseCommit {
		return fmt.Errorf("existing tag %s does not target release commit", w.tag)
	}
	return nil
}

// adoptSourcePublish validates the remote branch and tag without pushing.
func (w *releaseWorkflow) adoptSourcePublish(ctx context.Context) error {
	remoteHead, remoteTag, err := w.sourceRemoteState(ctx)
	if err != nil {
		return err
	}
	if remoteHead != w.state.ReleaseCommit || remoteTag != w.state.ReleaseCommit {
		return errors.New("remote source branch or tag does not match the release commit")
	}
	return nil
}

// validateAdoptedReleaseCommit requires the release commit's cli.go to
// carry the target version.
func (w *releaseWorkflow) validateAdoptedReleaseCommit(ctx context.Context, commit string) error {
	version, err := w.commitBinaryVersion(ctx, commit)
	if err != nil {
		return fmt.Errorf("read release commit binaryVersion: %w", err)
	}
	if version != w.version {
		return fmt.Errorf("release commit %s carries binaryVersion %q, want %q", shortOID(commit), version, w.version)
	}
	return nil
}

// commitBinaryVersion reads and parses the binaryVersion recorded in a
// commit's internal/cli/cli.go.
func (w *releaseWorkflow) commitBinaryVersion(ctx context.Context, rev string) (string, error) {
	content, err := w.proc.capture(ctx, w.root, nil, "git", "show", rev+":internal/cli/cli.go")
	if err != nil {
		return "", err
	}
	return binaryVersionFromContent([]byte(content))
}
