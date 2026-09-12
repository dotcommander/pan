package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const repositoryDiscoveryLimit = 4096

func prepareRepositoryTarget(root *Root, args []string, envRepo string) error {
	if root.AllRepos && root.Standalone {
		return errors.New("--all and --standalone are mutually exclusive")
	}
	// An explicit --repo/PAN_REPO already supplies a deliberate scan boundary.
	// Preserve that long-standing contract for fixtures and source-only trees.
	if explicitRepository(args, envRepo) {
		return nil
	}
	abs, err := filepath.Abs(root.Repo)
	if err != nil {
		return fmt.Errorf("resolve repository target: %w", err)
	}
	if root.Standalone {
		root.Repo = abs
		return nil
	}
	if repo, ok := containingRepository(abs); ok {
		root.Repo = repo
		return nil
	}
	repositories, limited, err := childRepositories(abs)
	if err != nil {
		return fmt.Errorf("discover repositories: %w", err)
	}
	if root.AllRepos {
		root.Repo = abs
		return nil
	}
	if len(repositories) == 0 {
		if limited {
			return fmt.Errorf("%s is not a Git repository; repository discovery stopped after %d directories; use --repo <path>, --all, or --standalone", abs, repositoryDiscoveryLimit)
		}
		return fmt.Errorf("%s is not a Git repository; use --repo <path> or --standalone", abs)
	}
	shown := repositories
	if len(shown) > 12 {
		shown = shown[:12]
	}
	lines := make([]string, 0, len(shown))
	for _, repository := range shown {
		relative, relErr := filepath.Rel(abs, repository)
		if relErr != nil {
			relative = repository
		}
		lines = append(lines, "  "+filepath.ToSlash(relative))
	}
	suffix := ""
	if len(repositories) > len(shown) {
		suffix = fmt.Sprintf("\n  ... and %d more", len(repositories)-len(shown))
	}
	return fmt.Errorf("%s contains %d Git repositories; no repository selected:\n%s%s\nuse --repo <path> for one repository, --all for the complete parent tree, or --standalone for loose source", abs, len(repositories), strings.Join(lines, "\n"), suffix)
}

func explicitRepository(args []string, envRepo string) bool {
	if envRepo != "" {
		return true
	}
	for i, arg := range args {
		if arg == "--repo" && i+1 < len(args) || strings.HasPrefix(arg, "--repo=") {
			return true
		}
	}
	return false
}

func containingRepository(path string) (string, bool) {
	for current := path; ; current = filepath.Dir(current) {
		if gitMarker(current) {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
	}
}

func childRepositories(root string) ([]string, bool, error) {
	var repositories []string
	directories := 0
	limited := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root {
			directories++
			if directories > repositoryDiscoveryLimit {
				limited = true
				return fs.SkipAll
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			if gitMarker(path) {
				repositories = append(repositories, path)
				return filepath.SkipDir
			}
		}
		return nil
	})
	sort.Strings(repositories)
	return repositories, limited, err
}

func gitMarker(root string) bool {
	info, err := os.Lstat(filepath.Join(root, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}
