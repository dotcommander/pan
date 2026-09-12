package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	contextHookMarker = "# pan post-commit hook"
	contextHookScript = "#!/bin/sh\n" + contextHookMarker + "\ncommand -v pan >/dev/null 2>&1 && pan cache warm >/dev/null 2>&1 || true\n"
)

// ContextInitResult describes repository-local compatibility scaffolding.
type ContextInitResult struct {
	Directory string   `json:"directory"`
	Written   []string `json:"written,omitempty"`
	Skipped   []string `json:"skipped,omitempty"`
}

func runContextInit(directory string, force, noHook, noConfig bool) (ContextInitResult, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return ContextInitResult{}, fmt.Errorf("resolve repository directory: %w", err)
	}
	result := ContextInitResult{Directory: root}
	_ = noConfig
	if noHook {
		return result, nil
	}
	if err := writeContextHook(root, force, &result); err != nil {
		return result, err
	}
	return result, nil
}

func writeContextHook(root string, force bool, result *ContextInitResult) error {
	hooks := filepath.Join(root, ".git", "hooks")
	if _, err := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		result.Skipped = append(result.Skipped, "post-commit (not a git repository)")
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect git directory: %w", err)
	}
	if err := os.MkdirAll(hooks, 0o750); err != nil {
		return fmt.Errorf("create git hooks directory: %w", err)
	}
	hook := filepath.Join(hooks, "post-commit")
	current, err := os.ReadFile(hook)
	if err == nil && !strings.Contains(string(current), contextHookMarker) && !force {
		return fmt.Errorf("refusing to replace existing post-commit hook %s; use --force", hook)
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read post-commit hook: %w", err)
	}
	if err := writePrivateFile(hook, []byte(contextHookScript), 0o755); err != nil {
		return fmt.Errorf("write post-commit hook: %w", err)
	}
	result.Written = append(result.Written, hook)
	return nil
}

func writePrivateFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
