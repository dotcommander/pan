package gitoutgoing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableFormatDoesNotTreatJavaClassAsMachO(t *testing.T) {
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

func TestClassifyPathReportsGenericProtectedPaths(t *testing.T) {
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

func TestInspectReportsGenericPathAndBlobEvidence(t *testing.T) {
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
	rules := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		rules = append(rules, finding.Rule+":"+finding.Path)
	}
	if got := strings.Join(rules, ","); got != "environment-file:.env,large-blob:.env,large-blob:large.txt" {
		t.Fatalf("findings = %q", got)
	}
}

func TestInspectRejectsNegativeBlobLimit(t *testing.T) {
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

func writeOutgoingFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
