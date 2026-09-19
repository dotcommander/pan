package gitoutgoing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableFormatDoesNotTreatJavaClassAsMachO(t *testing.T) {
	t.Parallel()
	javaClass := []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 0x3d}
	if got := executableFormat(javaClass); got != "" {
		t.Fatalf("executableFormat(java class) = %q, want empty", got)
	}
	fatMachO := make([]byte, 28)
	copy(fatMachO, []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 1})
	if got := executableFormat(fatMachO); got != "Mach-O" {
		t.Fatalf("executableFormat(fat Mach-O) = %q, want Mach-O", got)
	}
	swappedFatMachO := make([]byte, 28)
	copy(swappedFatMachO, []byte{0xbe, 0xba, 0xfe, 0xca, 1, 0, 0, 0})
	if got := executableFormat(swappedFatMachO); got != "Mach-O" {
		t.Fatalf("executableFormat(swapped fat Mach-O) = %q, want Mach-O", got)
	}
}

func TestExecutableFormatRecognizesSwappedMachO(t *testing.T) {
	t.Parallel()
	if got := executableFormat([]byte{0xce, 0xfa, 0xed, 0xfe}); got != "Mach-O" {
		t.Fatalf("executableFormat(swapped Mach-O) = %q, want Mach-O", got)
	}
}

func TestClassifyPathReportsGenericProtectedPaths(t *testing.T) {
	t.Parallel()
	if findings := classifyPath("commit", ".work/private.txt"); len(findings) != 1 || findings[0].Rule != "private-work-directory" {
		t.Fatalf("shared protected path findings = %#v", findings)
	}
	if findings := classifyPath("commit", "data/private.json"); len(findings) != 0 {
		t.Fatalf("unscoped data findings = %#v", findings)
	}
	if findings := classifyPath("commit", ".env.example"); len(findings) != 0 {
		t.Fatalf("environment template findings = %#v", findings)
	}
}

func TestParseRawLogSplitsCommitsAndRecords(t *testing.T) {
	t.Parallel()
	stream := []byte("\x00aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x00\x00\n" +
		":000000 100644 0000000000000000000000000000000000000000 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb A\x00" +
		".env\x00\x00cccccccccccccccccccccccccccccccccccccccc\x00\x00\n" +
		":100644 100644 dddddddddddddddddddddddddddddddddddddddd eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee M\x00" +
		"docs/read me.md\x00")
	commits, err := parseRawLog(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	if commits[0].oid != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || len(commits[0].records) != 1 {
		t.Fatalf("first commit = %#v", commits[0])
	}
	if string(commits[1].records[0].path) != "docs/read me.md" {
		t.Fatalf("second commit path = %q", commits[1].records[0].path)
	}
}

func TestParseRawLogPreservesControlBytesInPaths(t *testing.T) {
	t.Parallel()
	stream := []byte("\x00aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x00\x00\n" +
		":000000 100644 0000000000000000000000000000000000000000 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb A\x00" +
		"control-\x01-name\x00")
	commits, err := parseRawLog(stream)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(commits[0].records[0].path); got != "control-\x01-name" {
		t.Fatalf("path = %q, want control byte preserved", got)
	}
}

func TestParseRawLogRejectsMalformedStreams(t *testing.T) {
	t.Parallel()
	if _, err := parseRawLog([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x00:meta only-without-path")); err == nil {
		t.Fatal("parseRawLog accepted an odd record count")
	}
	if _, err := parseRawLog([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x00not-a-meta-record\x00path\x00")); err == nil {
		t.Fatal("parseRawLog accepted a record without a : prefix")
	}
	if _, err := parseRawLog([]byte(":meta\x00path\x00")); err == nil {
		t.Fatal("parseRawLog accepted a record before a commit id")
	}
}

func TestInspectReportsGenericPathAndBlobEvidence(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Outgoing Test")
	writeOutgoingFile(t, filepath.Join(repo, ".env"), "credential=value\n")
	writeOutgoingFile(t, filepath.Join(repo, "large.txt"), "oversized\n")
	runGit(t, repo, "add", ".env", "large.txt")
	runGit(t, repo, "commit", "-m", "fixture")

	report, err := Inspect("HEAD", repo, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Blocked || report.PathsTruncated || len(report.Paths) != 2 {
		t.Fatalf("unexpected report summary: %#v", report)
	}
	head := revisionAt(t, repo, "HEAD")
	for _, evidence := range report.Paths {
		if evidence.Commit != head {
			t.Fatalf("path evidence commit = %q, want %q", evidence.Commit, head)
		}
	}
	for _, finding := range report.Findings {
		if finding.Commit != head {
			t.Fatalf("finding commit = %q, want %q", finding.Commit, head)
		}
	}
	rules := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		rules = append(rules, finding.Rule+":"+finding.Path)
	}
	if got := strings.Join(rules, ","); got != "environment-file:.env,large-blob:.env,large-blob:large.txt" {
		t.Fatalf("findings = %q", got)
	}
}

func TestInspectScansOnlyCommitsInsideRange(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Outgoing Test")
	writeOutgoingFile(t, filepath.Join(repo, ".env"), "credential=value\n")
	runGit(t, repo, "add", ".env")
	runGit(t, repo, "commit", "-m", "secret base")
	base := revisionAt(t, repo, "HEAD")
	writeOutgoingFile(t, filepath.Join(repo, "safe.txt"), "public\n")
	runGit(t, repo, "add", "safe.txt")
	runGit(t, repo, "commit", "-m", "safe delta")

	report, err := Inspect(base+"..HEAD", repo, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked {
		t.Fatalf("range scan reported pre-base history: %#v", report.Findings)
	}
	if len(report.Paths) != 1 || report.Paths[0].Path != "safe.txt" {
		t.Fatalf("range paths = %#v", report.Paths)
	}
}

func TestInspectFlagsExecutableBlobContent(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Outgoing Test")
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 128)...)
	if err := os.WriteFile(filepath.Join(repo, "tool"), elf, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tool")
	runGit(t, repo, "commit", "-m", "add binary")

	report, err := Inspect("HEAD", repo, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Blocked || len(report.Findings) != 1 || report.Findings[0].Rule != "executable-content" {
		t.Fatalf("findings = %#v", report.Findings)
	}
}

func TestBlobSizesRejectsUnavailableObject(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	missing := strings.Repeat("0", 40)
	if _, err := blobSizes(repo, []string{missing}); err == nil {
		t.Fatal("blobSizes accepted a missing object id")
	}
}

func TestInspectRejectsNegativeBlobLimit(t *testing.T) {
	t.Parallel()
	if _, err := Inspect("HEAD", t.TempDir(), -1); err == nil {
		t.Fatal("Inspect accepted a negative blob limit")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func revisionAt(t *testing.T, dir, rev string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", rev)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", rev, err)
	}
	return strings.TrimSpace(string(output))
}

func writeOutgoingFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
