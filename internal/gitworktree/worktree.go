// Package gitworktree builds a bounded, read-only inventory of local Git state.
package gitworktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultMaxItems       = 80
	DefaultLargeFileBytes = 1_048_576
)

var StableDiffOptions = []string{"--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/"}

var secretPatterns = []struct {
	label string
	match *regexp.Regexp
}{
	{"private-key-marker", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
	{"credential-assignment", regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|token|password|credential|private[_-]?key)\b\s*[:=]`)},
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
}

var artifactParts = map[string]struct{}{
	".env": {}, ".idea": {}, ".vscode": {}, ".work": {}, "node_modules": {}, "__pycache__": {},
}

var conflictCodes = map[string]struct{}{
	"DD": {}, "AU": {}, "UD": {}, "UA": {}, "DU": {}, "AA": {}, "UU": {},
}

type Options struct {
	Path           string
	Base           string
	MaxItems       int
	LargeFileBytes int64
}

type CommandResult struct {
	Code   int
	Stdout string
	Stderr string
}

// Build returns a JSON-compatible manifest and its documented exit code.
func Build(options Options) (map[string]any, int) {
	path, err := absolutePath(options.Path)
	if err != nil {
		return errorManifest(options.Path, "path is not a directory"), 2
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return errorManifest(path, "path is not a directory"), 2
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errorManifest(path, "git is not installed"), 1
	}

	rootText := gitText(path, "rev-parse", "--show-toplevel")
	if rootText == "" {
		return errorManifest(path, "not a Git repository"), 1
	}
	root, err := absolutePath(rootText)
	if err != nil {
		return errorManifest(path, "not a Git repository"), 1
	}
	statusResult := runGit(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if statusResult.Code != 0 {
		return map[string]any{
			"path": path, "repository_root": root, "ok": false,
			"error":              "required git status inventory failed: " + orUnknown(statusResult.Stderr),
			"mutation_performed": false,
		}, 1
	}

	status := parseStatus(statusResult.Stdout, options.MaxItems)
	branch := gitText(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	headOID := verifyCommit(root, "HEAD")
	upstream := gitText(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	upstreamOID := verifyCommit(root, upstream)
	defaultBranch := defaultBranch(root)
	baseRef := options.Base
	if baseRef == "" {
		baseRef = defaultBranch
	}
	if baseRef == "" {
		baseRef = upstream
	}
	baseOID := verifyCommit(root, baseRef)
	notes := []string{}
	skipped := []string{"remote freshness not checked; helper never fetches"}
	if options.Base != "" && baseOID == "" {
		notes = append(notes, "requested base ref is not a local commit: "+options.Base)
		baseRef = ""
	}
	diffRange := ""
	if baseRef != "" && baseOID != "" && headOID != "" {
		diffRange = baseRef + "...HEAD"
	}

	var ahead, behind any
	if upstream != "" {
		counts := strings.Fields(gitText(root, "rev-list", "--left-right", "--count", "--end-of-options", "HEAD..."+upstream))
		if len(counts) == 2 {
			a, aErr := strconv.Atoi(counts[0])
			b, bErr := strconv.Atoi(counts[1])
			if aErr == nil && bErr == nil {
				ahead, behind = a, b
			}
		}
	}

	committedFiles, committedTruncated, committedErr := nameOnly(root, diffRange, options.MaxItems)
	allPathsSet := map[string]struct{}{}
	for _, name := range committedFiles {
		allPathsSet[name] = struct{}{}
	}
	for _, entry := range status.Entries {
		allPathsSet[entry.Path] = struct{}{}
	}
	allPaths := sortedKeys(allPathsSet)
	largeFiles := []map[string]any{}
	for _, relative := range allPaths {
		if size, ok := fileSize(root, relative); ok && size > options.LargeFileBytes {
			largeFiles = append(largeFiles, map[string]any{"path": relative, "size_bytes": size})
		}
	}
	sort.Slice(largeFiles, func(i, j int) bool {
		left, right := largeFiles[i], largeFiles[j]
		if left["size_bytes"].(int64) != right["size_bytes"].(int64) {
			return left["size_bytes"].(int64) > right["size_bytes"].(int64)
		}
		return left["path"].(string) < right["path"].(string)
	})

	scanners := detectScanners(root)
	findings, scanErrors := secretFindings(root, diffRange, status.Untracked, options.MaxItems)
	artifactPaths := artifactFlags(allPaths)
	secretScanTruncated := len(findings) >= options.MaxItems || status.UntrackedCount > len(status.Untracked)
	if len(scanners) == 0 {
		skipped = append(skipped, "no configured gitleaks, detect-secrets, or trufflehog surface detected")
	}
	if len(findings) > 0 {
		notes = append(notes, "secret findings are redacted heuristic leads, not proof of a secret")
	}
	if status.Truncated {
		notes = append(notes, "status and untracked-file risk inventories are capped; raise --max-items for full path coverage")
	}
	if committedTruncated {
		notes = append(notes, "committed-range inventory is capped; raise --max-items for full path coverage")
	}
	if len(artifactPaths) > options.MaxItems || secretScanTruncated {
		notes = append(notes, "artifact or secret risk inventory is capped; raise --max-items for full path coverage")
	}
	requiredErrors := []string{}
	if committedErr != "" {
		requiredErrors = append(requiredErrors, committedErr)
	}
	requiredErrors = append(requiredErrors, scanErrors...)

	data := map[string]any{
		"ok":                                len(requiredErrors) == 0,
		"error":                             nil,
		"path":                              path,
		"repository_root":                   root,
		"branch":                            nilIfEmpty(branch),
		"detached_head":                     branch == "",
		"head_oid":                          nilIfEmpty(headOID),
		"default_branch":                    nilIfEmpty(defaultBranch),
		"upstream":                          nilIfEmpty(upstream),
		"upstream_oid":                      nilIfEmpty(upstreamOID),
		"base_ref":                          nilIfEmpty(baseRef),
		"base_oid":                          nilIfEmpty(baseOID),
		"diff_range":                        nilIfEmpty(diffRange),
		"ahead":                             ahead,
		"behind":                            behind,
		"status":                            status.asMap(),
		"committed_changed_files":           committedFiles,
		"committed_changed_files_truncated": committedTruncated,
		"large_file_threshold_bytes":        options.LargeFileBytes,
		"large_files":                       limitMaps(largeFiles, options.MaxItems),
		"risk_inventory_truncated":          status.Truncated || committedTruncated || len(largeFiles) > options.MaxItems || len(artifactPaths) > options.MaxItems || secretScanTruncated,
		"artifact_flags":                    limitStrings(artifactPaths, options.MaxItems),
		"configured_secret_scanners":        scanners,
		"secret_scan": map[string]any{
			"classification": func() string {
				if len(scanErrors) > 0 {
					return "failed-closed"
				}
				return "heuristic-redacted"
			}(),
			"findings":  findings,
			"truncated": secretScanTruncated,
		},
		"notes":              notes,
		"skipped_checks":     skipped,
		"mutation_performed": false,
	}
	if len(requiredErrors) > 0 {
		data["error"] = strings.Join(requiredErrors, "; ")
		return data, 1
	}
	return data, 0
}

func errorManifest(path, message string) map[string]any {
	return map[string]any{"path": path, "ok": false, "error": message, "mutation_performed": false}
}

func absolutePath(path string) (string, error) {
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		return resolved, nil
	}
	return filepath.Clean(abs), nil
}

func runGit(path string, args ...string) CommandResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return CommandResult{Code: 124, Stderr: "git " + strings.Join(args, " ") + " timed out"}
	}
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			return CommandResult{Code: 127, Stderr: "git is not installed"}
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return CommandResult{Code: exitErr.ExitCode(), Stdout: stdout.String(), Stderr: strings.TrimSpace(stderr.String())}
		}
		return CommandResult{Code: 1, Stdout: stdout.String(), Stderr: strings.TrimSpace(stderr.String())}
	}
	return CommandResult{Code: 0, Stdout: stdout.String(), Stderr: strings.TrimSpace(stderr.String())}
}

func gitText(path string, args ...string) string {
	result := runGit(path, args...)
	if result.Code != 0 {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

func defaultBranch(path string) string {
	if symbolic := gitText(path, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); symbolic != "" {
		return symbolic
	}
	for _, candidate := range []struct {
		name string
		ref  string
	}{
		{name: "origin/main", ref: "refs/remotes/origin/main"},
		{name: "origin/master", ref: "refs/remotes/origin/master"},
		{name: "main", ref: "refs/heads/main"},
		{name: "master", ref: "refs/heads/master"},
	} {
		if runGit(path, "show-ref", "--verify", "--quiet", candidate.ref).Code == 0 {
			return candidate.name
		}
	}
	return ""
}

func verifyCommit(path, ref string) string {
	if ref == "" {
		return ""
	}
	return gitText(path, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
}

func nameOnly(path, diffRange string, maxItems int) ([]string, bool, string) {
	if diffRange == "" {
		return []string{}, false, ""
	}
	args := append([]string{"diff"}, StableDiffOptions...)
	args = append(args, "--name-only", "-z", diffRange, "--")
	result := runGit(path, args...)
	if result.Code != 0 {
		return []string{}, false, orUnknown(result.Stderr, "committed-range diff failed")
	}
	set := map[string]struct{}{}
	for _, name := range strings.Split(result.Stdout, "\x00") {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	names := sortedKeys(set)
	return limitStrings(names, maxItems), len(names) > maxItems, ""
}

type statusEntry struct {
	Code         string `json:"code"`
	Path         string `json:"path"`
	OriginalPath string `json:"original_path,omitempty"`
}

type statusInventory struct {
	Entries         []statusEntry
	Staged          []string
	Unstaged        []string
	Untracked       []string
	Conflicted      []string
	EntryCount      int
	StagedCount     int
	UnstagedCount   int
	UntrackedCount  int
	ConflictedCount int
	Truncated       bool
}

func parseStatus(raw string, maxItems int) statusInventory {
	entries := []statusEntry{}
	parts := strings.Split(raw, "\x00")
	for index := 0; index < len(parts); index++ {
		record := parts[index]
		if record == "" || len(record) < 3 {
			continue
		}
		entry := statusEntry{Code: record[:2], Path: record[3:]}
		if (strings.Contains(entry.Code, "R") || strings.Contains(entry.Code, "C")) && index+1 < len(parts) {
			index++
			entry.OriginalPath = parts[index]
		}
		entries = append(entries, entry)
	}
	stagedSet, unstagedSet, untrackedSet, conflictedSet := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, entry := range entries {
		code := entry.Code
		if code != "??" && code != "!!" && code[0] != ' ' {
			stagedSet[entry.Path] = struct{}{}
		}
		if code != "??" && code != "!!" && code[1] != ' ' {
			unstagedSet[entry.Path] = struct{}{}
		}
		if code == "??" {
			untrackedSet[entry.Path] = struct{}{}
		}
		if _, ok := conflictCodes[code]; ok {
			conflictedSet[entry.Path] = struct{}{}
		}
	}
	staged, unstaged := sortedKeys(stagedSet), sortedKeys(unstagedSet)
	untracked, conflicted := sortedKeys(untrackedSet), sortedKeys(conflictedSet)
	return statusInventory{
		Entries: limitEntries(entries, maxItems), Staged: limitStrings(staged, maxItems), Unstaged: limitStrings(unstaged, maxItems), Untracked: limitStrings(untracked, maxItems), Conflicted: limitStrings(conflicted, maxItems),
		EntryCount: len(entries), StagedCount: len(staged), UnstagedCount: len(unstaged), UntrackedCount: len(untracked), ConflictedCount: len(conflicted), Truncated: len(entries) > maxItems,
	}
}

func (s statusInventory) asMap() map[string]any {
	entries := make([]map[string]string, 0, len(s.Entries))
	for _, entry := range s.Entries {
		item := map[string]string{"code": entry.Code, "path": entry.Path}
		if entry.OriginalPath != "" {
			item["original_path"] = entry.OriginalPath
		}
		entries = append(entries, item)
	}
	return map[string]any{"entries": entries, "entry_count": s.EntryCount, "staged": s.Staged, "staged_count": s.StagedCount, "unstaged": s.Unstaged, "unstaged_count": s.UnstagedCount, "untracked": s.Untracked, "untracked_count": s.UntrackedCount, "conflicted": s.Conflicted, "conflicted_count": s.ConflictedCount, "truncated": s.Truncated}
}

func safeRepoFile(root, relative string) (string, string) {
	if filepath.IsAbs(relative) {
		return "", "absolute paths are not allowed"
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "repository root cannot be resolved"
	}
	lexical := filepath.Clean(filepath.Join(canonicalRoot, relative))
	rel, err := filepath.Rel(canonicalRoot, lexical)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "path escapes repository root"
	}
	current := canonicalRoot
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return "", "path does not exist"
		}
		if err != nil {
			return "", "path cannot be safely resolved inside repository root"
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "symlink components are not allowed"
		}
	}
	info, err := os.Lstat(current)
	if err != nil || !info.Mode().IsRegular() {
		return "", "path is not a regular file"
	}
	canonical, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", "path cannot be safely resolved inside repository root"
	}
	contained, err := filepath.Rel(canonicalRoot, canonical)
	if err != nil || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return "", "path cannot be safely resolved inside repository root"
	}
	return canonical, ""
}

func fileSize(root, relative string) (int64, bool) {
	if target, _ := safeRepoFile(root, relative); target != "" {
		if info, err := os.Lstat(target); err == nil {
			return info.Size(), true
		}
	}
	value := gitText(root, "cat-file", "-s", "HEAD:"+relative)
	size, err := strconv.ParseInt(value, 10, 64)
	return size, err == nil
}

func detectScanners(root string) []string {
	scanners := map[string]struct{}{}
	if target, _ := safeRepoFile(root, ".gitleaks.toml"); target != "" {
		scanners["gitleaks"] = struct{}{}
	}
	if target, _ := safeRepoFile(root, ".detect-secrets.cfg"); target != "" {
		scanners["detect-secrets"] = struct{}{}
	}
	if target, _ := safeRepoFile(root, ".secrets.baseline"); target != "" {
		scanners["detect-secrets"] = struct{}{}
	}
	if target, _ := safeRepoFile(root, ".trufflehog.yaml"); target != "" {
		scanners["trufflehog"] = struct{}{}
	}
	if target, _ := safeRepoFile(root, ".pre-commit-config.yaml"); target != "" {
		if content, err := os.ReadFile(target); err == nil {
			text := strings.ToLower(string(content))
			for _, name := range []string{"gitleaks", "detect-secrets", "trufflehog"} {
				if strings.Contains(text, name) {
					scanners[name] = struct{}{}
				}
			}
		}
	}
	return sortedKeys(scanners)
}

func scanAddedLines(diffText, source string, findings map[string]map[string]any, maxItems int) {
	path := "unknown"
	lineNumber := 0
	for _, line := range strings.Split(diffText, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "+++ b/") {
			path = line[6:]
			continue
		}
		if strings.HasPrefix(line, "@@") {
			match := regexp.MustCompile(`\+(\d+)`).FindStringSubmatch(line)
			lineNumber = 0
			if len(match) == 2 {
				lineNumber, _ = strconv.Atoi(match[1])
			}
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			content := line[1:]
			for _, pattern := range secretPatterns {
				if pattern.match.MatchString(content) {
					key := fmt.Sprintf("%s\x00%d\x00%s", path, lineNumber, pattern.label)
					findings[key] = map[string]any{"path": path, "line": nilIfZero(lineNumber), "pattern": pattern.label, "source": source}
					if len(findings) >= maxItems {
						return
					}
				}
			}
			if lineNumber != 0 {
				lineNumber++
			}
		} else if !strings.HasPrefix(line, "-") && lineNumber != 0 {
			lineNumber++
		}
	}
}

func scanUntracked(root string, paths []string, findings map[string]map[string]any, maxItems int) []string {
	failures := []string{}
	for _, relative := range paths {
		if len(findings) >= maxItems {
			return failures
		}
		target, reason := safeRepoFile(root, relative)
		if target == "" {
			if len(failures) < maxItems {
				failures = append(failures, "untracked-file "+relative+": "+orUnknown(reason, "unsafe path"))
			}
			continue
		}
		info, err := os.Lstat(target)
		if err != nil || info.Size() > DefaultLargeFileBytes {
			continue
		}
		content, err := os.ReadFile(target)
		if err != nil {
			if len(failures) < maxItems {
				failures = append(failures, "untracked-file "+relative+": read failed")
			}
			continue
		}
		for number, line := range strings.Split(string(content), "\n") {
			for _, pattern := range secretPatterns {
				if pattern.match.MatchString(line) {
					key := fmt.Sprintf("%s\x00%d\x00%s", relative, number+1, pattern.label)
					findings[key] = map[string]any{"path": relative, "line": number + 1, "pattern": pattern.label, "source": "untracked-file"}
					if len(findings) >= maxItems {
						return failures
					}
				}
			}
		}
	}
	return failures
}

func secretFindings(root, diffRange string, untracked []string, maxItems int) ([]map[string]any, []string) {
	findings := map[string]map[string]any{}
	failures := []string{}
	commands := []struct {
		source string
		args   []string
	}{
		{"working-tree", append(append([]string{"diff"}, StableDiffOptions...), "--unified=0", "--no-color")},
		{"staged", append(append([]string{"diff"}, StableDiffOptions...), "--cached", "--unified=0", "--no-color")},
	}
	if diffRange != "" {
		commands = append(commands, struct {
			source string
			args   []string
		}{"committed-range", append(append([]string{"diff"}, StableDiffOptions...), "--unified=0", "--no-color", diffRange)})
	}
	for _, command := range commands {
		result := runGit(root, command.args...)
		if result.Code == 0 {
			scanAddedLines(result.Stdout, command.source, findings, maxItems)
		} else {
			failures = append(failures, command.source+": "+orUnknown(result.Stderr, "git diff failed"))
		}
		if len(findings) >= maxItems {
			break
		}
	}
	failures = append(failures, scanUntracked(root, untracked, findings, maxItems)...)
	keys := sortedKeys(findings)
	ordered := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		ordered = append(ordered, findings[key])
	}
	sortFindings(ordered)
	return limitMaps(ordered, maxItems), failures
}

func sortFindings(ordered []map[string]any) {
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left["path"].(string) != right["path"].(string) {
			return left["path"].(string) < right["path"].(string)
		}
		leftLine, rightLine := findingLine(left), findingLine(right)
		if leftLine != rightLine {
			return leftLine < rightLine
		}
		if left["pattern"].(string) != right["pattern"].(string) {
			return left["pattern"].(string) < right["pattern"].(string)
		}
		return left["source"].(string) < right["source"].(string)
	})
}

func findingLine(finding map[string]any) int {
	if value, ok := finding["line"].(int); ok {
		return value
	}
	return 0
}

func artifactFlags(paths []string) []string {
	flags := map[string]struct{}{}
	for _, value := range paths {
		base := filepath.Base(value)
		if strings.HasPrefix(base, ".env") {
			flags[value] = struct{}{}
			continue
		}
		for _, part := range strings.Split(filepath.Clean(value), string(filepath.Separator)) {
			if _, ok := artifactParts[part]; ok {
				flags[value] = struct{}{}
				break
			}
		}
	}
	return sortedKeys(flags)
}

func limitStrings(values []string, maximum int) []string {
	if len(values) > maximum {
		return values[:maximum]
	}
	return values
}
func limitMaps(values []map[string]any, maximum int) []map[string]any {
	if len(values) > maximum {
		return values[:maximum]
	}
	return values
}
func limitEntries(values []statusEntry, maximum int) []statusEntry {
	if len(values) > maximum {
		return values[:maximum]
	}
	return values
}
func sortedKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nilIfZero(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
func orUnknown(value string, fallback ...string) string {
	if value != "" {
		return value
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return "unknown error"
}

// RenderMarkdown preserves the concise human-readable output of the retired helper.
func RenderMarkdown(data map[string]any) string {
	if ok, _ := data["ok"].(bool); !ok {
		return "# Git Operation Manifest\n\nError: " + orUnknown(fmt.Sprint(data["error"])) + "\n"
	}
	status := data["status"].(map[string]any)
	value := func(key, fallback string) string {
		if data[key] == nil {
			return fallback
		}
		return fmt.Sprint(data[key])
	}
	lines := []string{"# Git Operation Manifest", "", "- Repository: `" + value("repository_root", "unknown") + "`", "- Branch: `" + value("branch", "(detached HEAD)") + "`", "- HEAD: `" + value("head_oid", "unknown") + "`", "- Default branch: `" + value("default_branch", "unknown") + "`", "- Upstream: `" + value("upstream", "none") + "`", "- Base/diff: `" + value("diff_range", "unresolved") + "`", "- Ahead/behind upstream: `" + value("ahead", "unknown") + "` / `" + value("behind", "unknown") + "`", "", "## Working State", "", fmt.Sprintf("- Status entries: %v%s", status["entry_count"], suffix(status["truncated"].(bool), " (truncated)")), fmt.Sprintf("- Staged: %v", status["staged_count"]), fmt.Sprintf("- Unstaged: %v", status["unstaged_count"]), fmt.Sprintf("- Untracked: %v", status["untracked_count"]), fmt.Sprintf("- Conflicted: %v", status["conflicted_count"]), fmt.Sprintf("- Committed-range files: %d%s", len(data["committed_changed_files"].([]string)), suffix(data["committed_changed_files_truncated"].(bool), " (truncated)")), "", "## Risk Leads", "", fmt.Sprintf("- Large files: %d", len(data["large_files"].([]map[string]any))), fmt.Sprintf("- Artifact-like paths: %d", len(data["artifact_flags"].([]string))), "- Configured secret scanners: " + joinOrNone(data["configured_secret_scanners"].([]string)), fmt.Sprintf("- Redacted heuristic secret findings: %d", len(data["secret_scan"].(map[string]any)["findings"].([]map[string]any)))}
	for _, finding := range data["secret_scan"].(map[string]any)["findings"].([]map[string]any) {
		suffix := ""
		if finding["line"] != nil {
			suffix = fmt.Sprintf(":%v", finding["line"])
		}
		lines = append(lines, fmt.Sprintf("  - `%v%s`: `%v` (%v)", finding["path"], suffix, finding["pattern"], finding["source"]))
	}
	lines = append(lines, "", "## Notes")
	if notes := data["notes"].([]string); len(notes) > 0 {
		for _, note := range notes {
			lines = append(lines, "- "+note)
		}
	} else {
		lines = append(lines, "- none")
	}
	lines = append(lines, "", "## Skipped Checks")
	for _, item := range data["skipped_checks"].([]string) {
		lines = append(lines, "- "+item)
	}
	lines = append(lines, "", "No fetch, ref update, staging, commit, or file mutation was performed.")
	return strings.Join(lines, "\n") + "\n"
}

func suffix(condition bool, value string) string {
	if condition {
		return value
	}
	return ""
}
func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none detected"
	}
	return strings.Join(values, ", ")
}
