package repo

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	preflightAbsent = "absent"
	preflightFound  = "found"
)

// PreflightCheck records one repository-context path inspection.
type PreflightCheck struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

// PurposeSignal describes the nearest repository purpose document.
type PurposeSignal struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

// PreflightResult describes the instructions and purpose that apply to a target.
type PreflightResult struct {
	Target          string           `json:"target"`
	RepositoryRoot  string           `json:"repository_root"`
	Mode            string           `json:"mode"`
	Guidance        []string         `json:"guidance"`
	Purpose         *PurposeSignal   `json:"purpose"`
	Checks          []PreflightCheck `json:"checks"`
	ContextResolved bool             `json:"context_resolved"`
}

// Preflight resolves repository guidance and the nearest purpose signal for target.
// Standalone mode resolves only the target path. Repository mode requires target to
// remain within root, except when root exposes it through a direct child symlink.
func Preflight(target, root string, standalone bool) (PreflightResult, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return PreflightResult{}, err
	}
	targetPath := preflightTargetPath(target, root)
	if standalone {
		return PreflightResult{
			Target:          targetPath,
			RepositoryRoot:  rootAbs,
			Mode:            "standalone",
			Guidance:        []string{},
			Checks:          []PreflightCheck{},
			ContextResolved: true,
		}, nil
	}

	if info, err := os.Stat(rootAbs); err != nil || !info.IsDir() {
		return PreflightResult{}, fmt.Errorf("repository root must be a directory: %s", rootAbs)
	}

	dirs, err := preflightDirectories(root, target)
	if err != nil {
		return PreflightResult{}, err
	}
	guidance, checks := preflightGuidance(dirs)
	purpose, purposeChecks := preflightPurpose(dirs)
	checks = append(checks, purposeChecks...)

	return PreflightResult{
		Target:          targetPath,
		RepositoryRoot:  rootAbs,
		Mode:            "repository",
		Guidance:        guidance,
		Purpose:         purpose,
		Checks:          checks,
		ContextResolved: true,
	}, nil
}

func preflightTargetPath(target, root string) string {
	targetPath := target
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(root, targetPath)
	}
	targetPath, _ = filepath.Abs(targetPath)
	if resolved, err := filepath.EvalSymlinks(targetPath); err == nil {
		return resolved
	}
	return targetPath
}

func preflightGuidance(dirs []string) ([]string, []PreflightCheck) {
	checks := []PreflightCheck{}
	guidance := []string{}
	for _, dir := range dirs {
		candidate := filepath.Join(dir, "AGENTS.md")
		status := preflightAbsent
		if preflightRegularFile(candidate) {
			status = preflightFound
			guidance = append(guidance, candidate)
		}
		checks = append(checks, PreflightCheck{Kind: "agents", Path: candidate, Status: status})
	}
	if preflightAllowsClaude(guidance) {
		guidance, checks = preflightClaude(dirs, guidance, checks)
	}
	return guidance, checks
}

func preflightAllowsClaude(guidance []string) bool {
	if len(guidance) == 0 {
		return true
	}
	for _, path := range guidance {
		text, _ := preflightReadPrefix(path, 8192)
		if strings.Contains(text, "CLAUDE.md") {
			return true
		}
	}
	return false
}

func preflightClaude(dirs, guidance []string, checks []PreflightCheck) ([]string, []PreflightCheck) {
	for i := len(dirs) - 1; i >= 0; i-- {
		candidate := filepath.Join(dirs[i], "CLAUDE.md")
		status := preflightAbsent
		if preflightRegularFile(candidate) {
			status = preflightFound
			guidance = append(guidance, candidate)
		}
		checks = append(checks, PreflightCheck{Kind: "claude", Path: candidate, Status: status})
		if status == preflightFound {
			break
		}
	}
	return guidance, checks
}

func preflightPurpose(dirs []string) (*PurposeSignal, []PreflightCheck) {
	checks := []PreflightCheck{}
	for i := len(dirs) - 1; i >= 0; i-- {
		for _, name := range [...]string{"README.md", "README", "SPEC.md", "SPECIFICATION.md"} {
			candidate := filepath.Join(dirs[i], name)
			status := preflightAbsent
			if preflightRegularFile(candidate) {
				status = preflightFound
			}
			checks = append(checks, PreflightCheck{Kind: "purpose", Path: candidate, Status: status})
			if status == preflightFound {
				return &PurposeSignal{Kind: name, Path: candidate, Summary: preflightSummary(candidate)}, checks
			}
		}
	}
	return nil, checks
}

func preflightDirectories(root, target string) ([]string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	targetPath := target
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(root, targetPath)
	}
	info, statErr := os.Stat(targetPath)
	targetDir := filepath.Dir(targetPath)
	if statErr == nil && info.IsDir() {
		targetDir = targetPath
	}
	if rel, relErr := filepath.Rel(rootAbs, targetDir); relErr == nil && preflightInside(rel) {
		return preflightDirectoryChain(rootAbs, rel), nil
	}

	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		resolvedRoot = rootAbs
	}
	resolvedTarget, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		resolvedTarget = filepath.Clean(targetDir)
	}
	if rel, relErr := filepath.Rel(resolvedRoot, resolvedTarget); relErr == nil && preflightInside(rel) {
		return preflightDirectoryChain(resolvedRoot, rel), nil
	}

	if dirs, ok := preflightDirectChild(rootAbs, resolvedTarget); ok {
		return dirs, nil
	}
	return nil, errors.New("target must be within repository_root")
}

func preflightDirectChild(root, resolvedTarget string) ([]string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, false
	}
	for _, entry := range entries {
		child := filepath.Join(root, entry.Name())
		resolvedChild, resolveErr := filepath.EvalSymlinks(child)
		if resolveErr != nil {
			continue
		}
		childRel, relErr := filepath.Rel(resolvedChild, resolvedTarget)
		if relErr != nil || !preflightInside(childRel) {
			continue
		}
		dirs := []string{root, child}
		if childRel != "." {
			dirs = append(dirs, preflightDirectoryChain(child, childRel)[1:]...)
		}
		return dirs, true
	}
	return nil, false
}

func preflightRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func preflightInside(relative string) bool {
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func preflightDirectoryChain(root, relative string) []string {
	dirs := []string{root}
	current := root
	if relative != "." {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			dirs = append(dirs, current)
		}
	}
	return dirs
}

func preflightReadPrefix(path string, limit int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	buffer := make([]byte, limit)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buffer[:n]), nil
}

func preflightSummary(path string) string {
	text, _ := preflightReadPrefix(path, 8192)
	scanner := bufio.NewScanner(strings.NewReader(text))
	lines := []string{}
	body := []string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lines = append(lines, line)
		if line != "" && !strings.HasPrefix(line, "#") && len(body) < 3 {
			body = append(body, line)
		}
	}
	value := strings.Join(body, " ")
	if value == "" {
		for _, line := range lines {
			if line != "" {
				value = strings.TrimLeft(line, "# ")
				break
			}
		}
	}
	value = preflightCollapseWhitespace(value)
	if runes := []rune(value); len(runes) > 280 {
		value = string(runes[:280])
	}
	return value
}

func preflightCollapseWhitespace(value string) string {
	var collapsed strings.Builder
	collapsed.Grow(len(value))
	inWhitespace := false
	for i := range len(value) {
		switch value[i] {
		case ' ', '\t', '\n', '\f', '\r':
			if !inWhitespace {
				collapsed.WriteByte(' ')
				inWhitespace = true
			}
		default:
			collapsed.WriteByte(value[i])
			inWhitespace = false
		}
	}
	return collapsed.String()
}
