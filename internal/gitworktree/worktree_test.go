package gitworktree

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseStatusNULSafeRenameAndBoundedInventories(t *testing.T) {
	status := parseStatus("R  new\x00old\x00?? scratch\x00 M work\x00", 2)
	if status.EntryCount != 3 || status.StagedCount != 1 || status.UnstagedCount != 1 || status.UntrackedCount != 1 {
		t.Fatalf("unexpected counts: %+v", status)
	}
	if got := status.Entries[0]; got.Path != "new" || got.OriginalPath != "old" {
		t.Fatalf("rename = %+v, want new from old", got)
	}
	if !status.Truncated || len(status.Entries) != 2 {
		t.Fatalf("entries were not capped: %+v", status)
	}
}

func TestScanAddedLinesRedactsValuesAndPreservesLocations(t *testing.T) {
	findings := map[string]map[string]any{}
	scanAddedLines("diff --git a/config b/config\n--- a/config\n+++ b/config\n@@ -0,0 +1 @@\n+api_key = value-that-must-not-appear\n", "working-tree", findings, 10)
	if len(findings) != 1 {
		t.Fatalf("findings = %#v", findings)
	}
	for _, finding := range findings {
		if finding["path"] != "config" || finding["line"] != 1 || finding["pattern"] != "credential-assignment" {
			t.Fatalf("finding = %#v", finding)
		}
		encoded, _ := json.Marshal(finding)
		if strings.Contains(string(encoded), "value-that-must-not-appear") {
			t.Fatalf("secret value leaked: %s", encoded)
		}
	}
}

func TestSafeRepoFileRejectsSymlinkEscape(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	writeFile(t, outside, "api_key=escaped")
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	findings := map[string]map[string]any{}
	failures := scanUntracked(repo, []string{"escape"}, findings, 10)
	if len(findings) != 0 || !reflect.DeepEqual(failures, []string{"untracked-file escape: symlink components are not allowed"}) {
		t.Fatalf("findings=%#v failures=%#v", findings, failures)
	}
}

func TestBuildReportsRepositoryStateWithoutMutation(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	git(t, repo, "mv", "base.txt", "renamed.txt")
	writeFile(t, filepath.Join(repo, "renamed.txt"), strings.Repeat("x", 32)+"\n")
	writeFile(t, filepath.Join(repo, "untracked.env"), "token = should-not-leak\n")
	before := gitOutput(t, repo, "status", "--porcelain=v1", "-z")
	data, code := Build(Options{Path: repo, Base: base, MaxItems: 1, LargeFileBytes: 8})
	after := gitOutput(t, repo, "status", "--porcelain=v1", "-z")
	if code != 0 || data["ok"] != true || data["mutation_performed"] != false {
		t.Fatalf("code=%d data=%#v", code, data)
	}
	if before != after {
		t.Fatalf("manifest changed repository state: before=%q after=%q", before, after)
	}
	if data["committed_changed_files_truncated"] != false {
		t.Fatalf("unexpected committed truncation: %#v", data)
	}
	largeFiles := data["large_files"].([]map[string]any)
	if len(largeFiles) != 1 || largeFiles[0]["path"] != "renamed.txt" || largeFiles[0]["size_bytes"] != int64(33) {
		t.Fatalf("unexpected large files: %#v", largeFiles)
	}
	if !data["risk_inventory_truncated"].(bool) {
		t.Fatalf("expected capped risk inventory: %#v", data)
	}
	secretScan := data["secret_scan"].(map[string]any)
	findings := secretScan["findings"].([]map[string]any)
	if len(findings) != 1 || findings[0]["path"] != "untracked.env" || findings[0]["source"] != "untracked-file" {
		t.Fatalf("unexpected redacted findings: %#v", findings)
	}
	encoded, _ := json.Marshal(data)
	if strings.Contains(string(encoded), "should-not-leak") {
		t.Fatalf("secret value leaked into manifest: %s", encoded)
	}
	if !strings.Contains(RenderMarkdown(data), "# Git Operation Manifest") {
		t.Fatal("markdown output missing title")
	}
}

func TestBuildExitCodes(t *testing.T) {
	data, code := Build(Options{Path: filepath.Join(t.TempDir(), "missing"), MaxItems: 10, LargeFileBytes: 10})
	if code != 2 || data["error"] != "path is not a directory" || data["mutation_performed"] != false {
		t.Fatalf("non-directory code=%d data=%#v", code, data)
	}
	nonRepo := t.TempDir()
	data, code = Build(Options{Path: nonRepo, MaxItems: 10, LargeFileBytes: 10})
	if code != 1 || data["error"] != "not a Git repository" {
		t.Fatalf("non-repository code=%d data=%#v", code, data)
	}
}

func TestBuildCommittedRangeIncludesDeletedPaths(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "deleted.txt"), "gone\n")
	git(t, repo, "add", "deleted.txt")
	git(t, repo, "commit", "-m", "first")
	base := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	git(t, repo, "rm", "deleted.txt")
	git(t, repo, "commit", "-m", "delete")
	data, code := Build(Options{Path: repo, Base: base, MaxItems: 10, LargeFileBytes: 10})
	if code != 0 {
		t.Fatalf("code=%d data=%#v", code, data)
	}
	if got := data["committed_changed_files"].([]string); !reflect.DeepEqual(got, []string{"deleted.txt"}) {
		t.Fatalf("committed changed files = %#v", got)
	}
}

func TestDefaultBranchFallsBackToRemoteTrackingBranch(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, repo, "branch", "-m", "topic")

	if got := defaultBranch(repo); got != "origin/main" {
		t.Fatalf("defaultBranch() = %q, want origin/main", got)
	}
}

func TestCommittedRangeTruncationPropagatesToRiskAndMarkdown(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	for _, name := range []string{"a.txt", "b.txt"} {
		writeFile(t, filepath.Join(repo, name), name+"\n")
	}
	git(t, repo, "add", "a.txt", "b.txt")
	git(t, repo, "commit", "-m", "two files")
	data, code := Build(Options{Path: repo, Base: base, MaxItems: 1, LargeFileBytes: 1024})
	if code != 0 || data["committed_changed_files_truncated"] != true || data["risk_inventory_truncated"] != true {
		t.Fatalf("code=%d data=%#v", code, data)
	}
	markdown := RenderMarkdown(data)
	if !strings.Contains(markdown, "Committed-range files: 1 (truncated)") || !strings.Contains(markdown, "committed-range inventory is capped") {
		t.Fatalf("markdown did not disclose truncation:\n%s", markdown)
	}
}

func TestBuildFailsWhenGitIsUnavailable(t *testing.T) {
	path := t.TempDir()
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	data, code := Build(Options{Path: path, MaxItems: 10, LargeFileBytes: 10})
	if code != 1 || data["error"] != "git is not installed" {
		t.Fatalf("git absence code=%d data=%#v", code, data)
	}
	_ = oldPath
}

func TestArtifactFlagsAndStableDiffOptions(t *testing.T) {
	flags := artifactFlags([]string{".env.local", "x/node_modules/a", "plain.txt", "a/.vscode/settings.json"})
	if !reflect.DeepEqual(flags, []string{".env.local", "a/.vscode/settings.json", "x/node_modules/a"}) {
		t.Fatalf("artifact flags = %#v", flags)
	}
	for _, expected := range []string{"--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/"} {
		if !contains(StableDiffOptions, expected) {
			t.Fatalf("stable diff options omit %q", expected)
		}
	}
}

func TestDetectScannersAndFindingOrder(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".gitleaks.toml"), "")
	writeFile(t, filepath.Join(repo, ".pre-commit-config.yaml"), "repos: [trufflehog, detect-secrets]\n")
	if got := detectScanners(repo); !reflect.DeepEqual(got, []string{"detect-secrets", "gitleaks", "trufflehog"}) {
		t.Fatalf("scanners = %#v", got)
	}
	findings := map[string]map[string]any{}
	scanAddedLines("+++ b/z\n@@ -0,0 +10 @@\n+token=x\n+token=y\n", "working-tree", findings, 10)
	ordered := make([]map[string]any, 0, len(findings))
	for _, key := range sortedKeys(findings) {
		ordered = append(ordered, findings[key])
	}
	sortFindings(ordered)
	if len(ordered) != 2 || ordered[0]["line"] != 10 || ordered[1]["line"] != 11 {
		t.Fatalf("findings not numerically ordered: %#v", ordered)
	}
}

func newRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Manifest Test")
	return repo
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(output)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
