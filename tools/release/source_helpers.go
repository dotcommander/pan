package main

import (
	"context"
	"fmt"
	"strings"
)

func (w *releaseWorkflow) sourceRemoteState(ctx context.Context) (string, string, error) {
	output, err := w.proc.capture(ctx, w.root, nil, "git", "ls-remote", "origin",
		"refs/heads/main", "refs/tags/"+w.tag, "refs/tags/"+w.tag+"^{}")
	if err != nil {
		return "", "", err
	}
	var head, tag, peeledTag string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[1] {
		case "refs/heads/main":
			head = fields[0]
		case "refs/tags/" + w.tag:
			tag = fields[0]
		case "refs/tags/" + w.tag + "^{}":
			peeledTag = fields[0]
		}
	}
	if peeledTag != "" {
		tag = peeledTag
	}
	return head, tag, nil
}

func (w *releaseWorkflow) repositoryClean(ctx context.Context, dir string) (bool, error) {
	status, err := w.proc.capture(ctx, dir, nil, "git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return status == "", nil
}

func (w *releaseWorkflow) requireCleanRepository(ctx context.Context, dir string) error {
	clean, err := w.repositoryClean(ctx, dir)
	if err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("repository %s is dirty", dir)
	}
	return nil
}

func (w *releaseWorkflow) expectReleasePreparationPaths(ctx context.Context) error {
	unstaged, err := w.proc.capture(ctx, w.root, nil, "git", "diff", "--name-only", "-z")
	if err != nil {
		return err
	}
	untracked, err := w.proc.capture(ctx, w.root, nil, "git", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	paths := append(nulPaths(unstaged), nulPaths(untracked)...)
	if !samePaths(paths, "internal/cli/cli.go") {
		return fmt.Errorf("release preparation changed unexpected paths: %q", paths)
	}
	return nil
}
