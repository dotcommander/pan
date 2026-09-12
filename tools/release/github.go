package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type githubReleaseInfo struct {
	TagName    string `json:"tagName"`
	Draft      bool   `json:"isDraft"`
	Prerelease bool   `json:"isPrerelease"`
}

func (w *releaseWorkflow) githubRelease(ctx context.Context) error {
	release, exists, err := w.inspectGitHubRelease(ctx)
	if err != nil {
		return err
	}
	if exists {
		return w.validateGitHubRelease(release)
	}
	args := []string{
		"release", "create", w.tag,
		"--repo", sourceRepository,
		"--verify-tag",
		"--title", "Pan " + w.tag,
		"--generate-notes",
		"--latest",
	}
	if w.state.PreviousVersion != "" {
		args = append(args, "--notes-start-tag", "v"+w.state.PreviousVersion)
	}
	return w.proc.stream(ctx, w.root, nil, "gh", args...)
}

func (w *releaseWorkflow) verifyGitHubRelease(ctx context.Context) error {
	release, exists, err := w.inspectGitHubRelease(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("GitHub release is missing")
	}
	return w.validateGitHubRelease(release)
}

func (w *releaseWorkflow) inspectGitHubRelease(ctx context.Context) (githubReleaseInfo, bool, error) {
	output, code, err := w.proc.captureStatus(ctx, w.root, nil, "gh", "release", "view", w.tag,
		"--repo", sourceRepository, "--json", "tagName,isDraft,isPrerelease,url")
	if err != nil {
		return githubReleaseInfo{}, false, err
	}
	if code != 0 {
		if strings.Contains(strings.ToLower(output), "release not found") {
			return githubReleaseInfo{}, false, nil
		}
		return githubReleaseInfo{}, false, fmt.Errorf("inspect GitHub release: %s", strings.TrimSpace(output))
	}
	var release githubReleaseInfo
	if err := json.Unmarshal([]byte(output), &release); err != nil {
		return githubReleaseInfo{}, false, err
	}
	return release, true, nil
}

func (w *releaseWorkflow) validateGitHubRelease(release githubReleaseInfo) error {
	if release.TagName != w.tag || release.Draft || release.Prerelease {
		return errors.New("GitHub release does not match the requested stable tag")
	}
	return nil
}
