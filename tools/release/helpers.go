package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var binaryVersionPattern = regexp.MustCompile(`(?m)^var binaryVersion = "v([0-9]+\.[0-9]+\.[0-9]+)"$`)

type semanticVersion struct {
	major int
	minor int
	patch int
}

func normalizeVersion(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "VERSION=")
	raw = strings.TrimPrefix(raw, "v")
	version, err := parseVersion(raw)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", version.major, version.minor, version.patch), nil
}

func parseVersion(raw string) (semanticVersion, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("version %q must be MAJOR.MINOR.PATCH", raw)
	}
	values := [3]int{}
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, fmt.Errorf("version %q is not canonical semver", raw)
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return semanticVersion{}, fmt.Errorf("version %q is not canonical semver", raw)
		}
		values[index] = value
	}
	return semanticVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func compareVersions(left, right string) int {
	a, _ := parseVersion(left)
	b, _ := parseVersion(right)
	for _, pair := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func binaryVersionFromContent(content []byte) (string, error) {
	matches := binaryVersionPattern.FindAllSubmatch(content, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("expected one binaryVersion declaration, found %d", len(matches))
	}
	return string(matches[0][1]), nil
}

func replaceBinaryVersion(content []byte, previous, target string) ([]byte, error) {
	current, err := binaryVersionFromContent(content)
	if err != nil {
		return nil, err
	}
	if current == target {
		return content, nil
	}
	if current != previous {
		return nil, fmt.Errorf("binaryVersion is %s, expected %s", current, previous)
	}
	replacement := []byte(`var binaryVersion = "v` + target + `"`)
	return binaryVersionPattern.ReplaceAll(content, replacement), nil
}

func updateFormula(content []byte, archiveURL, archiveSHA string) ([]byte, error) {
	lines := strings.Split(string(content), "\n")
	urlCount := 0
	shaCount := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, `url "`):
			lines[index] = `  url "` + archiveURL + `"`
			urlCount++
		case strings.HasPrefix(trimmed, `sha256 "`):
			lines[index] = `  sha256 "` + archiveSHA + `"`
			shaCount++
		}
	}
	if urlCount != 1 || shaCount != 1 {
		return nil, fmt.Errorf("expected one formula url and sha256, found %d and %d", urlCount, shaCount)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func githubRepository(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@github.com:") {
		return strings.TrimSuffix(strings.TrimPrefix(remote, "git@github.com:"), ".git"), nil
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() != "github.com" {
		return "", fmt.Errorf("remote %q is not a GitHub repository", remote)
	}
	return strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git"), nil
}

func writeFileAtomic(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func nulPaths(raw string) []string {
	parts := strings.Split(raw, "\x00")
	return slices.DeleteFunc(parts, func(item string) bool { return item == "" })
}

func samePaths(observed []string, expected ...string) bool {
	slices.Sort(observed)
	slices.Sort(expected)
	return slices.Equal(observed, expected)
}

func releaseFetchArgs() []string {
	return []string{"fetch", "--no-prune", "--no-tags", "origin"}
}
