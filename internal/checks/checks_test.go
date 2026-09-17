package checks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillContractCompatibility(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		content  string
		status   Status
		message  string
		code     string
		wantLine int
	}{
		{name: "valid", content: "---\nname: valid-skill\ndescription: Valid skill.\nmetadata:\n  short-description: Valid\n---\n# Valid\n", status: StatusPassed},
		{name: "missing description", content: "---\nname: valid-skill\n---\n", status: StatusFailed, code: "skill.description.missing", message: "Missing 'description' in frontmatter", wantLine: 2},
		{name: "unexpected key", content: "---\nname: valid-skill\ndescription: Valid.\nextra: value\n---\n", status: StatusFailed, code: "skill.frontmatter.unexpected-key", message: "Unexpected key(s) in SKILL.md frontmatter: extra. Allowed properties are: allowed-tools, description, license, metadata, name", wantLine: 4},
		{name: "invalid name", content: "---\nname: InvalidSkill\ndescription: Valid.\n---\n", status: StatusFailed, code: "skill.name.format", message: "Name 'InvalidSkill' should be hyphen-case (lowercase letters, digits, and hyphens only)", wantLine: 2},
		{name: "wrong description type", content: "---\nname: valid-skill\ndescription: [not, text]\n---\n", status: StatusFailed, code: "skill.description.type", message: "Description must be a string, got list", wantLine: 3},
		{name: "body todo", content: "---\nname: valid-skill\ndescription: Valid.\n---\n[TODO: finish]\n", status: StatusFailed, code: "skill.body.todo", message: "Skill instructions contain an unfinished TODO placeholder", wantLine: 5},
		{name: "fenced todo", content: "---\nname: valid-skill\ndescription: Valid.\n---\n```text\n[TODO: example]\n```\n", status: StatusPassed},
	}
	registry := Builtins()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			report, err := registry.Run(context.Background(), Request{Root: root, Target: ".", CheckIDs: []string{"skill-contract"}})
			if err != nil {
				t.Fatal(err)
			}
			result := report.Checks[0]
			if result.Status != tt.status {
				t.Fatalf("status = %q, want %q", result.Status, tt.status)
			}
			if tt.status == StatusPassed {
				if len(result.Findings) != 0 {
					t.Fatalf("findings = %#v, want none", result.Findings)
				}
				return
			}
			finding := result.Findings[0]
			if finding.Code != tt.code || finding.Message != tt.message || finding.Line != tt.wantLine {
				t.Fatalf("finding = %#v", finding)
			}
		})
	}
}

func TestRegistryRejectsDuplicateProviderIDs(t *testing.T) {
	t.Parallel()
	_, err := NewRegistry(skillContractProvider{}, skillContractProvider{})
	if err == nil || !strings.Contains(err.Error(), "duplicate check provider ID") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunSkipsInapplicableCheck(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	report, err := Builtins().Run(context.Background(), Request{Root: root, Target: "."})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 1 || report.Checks[0].Status != StatusSkipped || len(report.Coverage.Skipped) != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestRunRejectsOutsideTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	_, err := Builtins().Run(context.Background(), Request{Root: root, Target: outside})
	if err == nil || !strings.Contains(err.Error(), "outside repository root") {
		t.Fatalf("error = %v", err)
	}
}

func TestSkillContractRejectsSymlinkOutsideRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(outside, []byte("---\nname: valid-skill\ndescription: Valid.\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	_, err := Builtins().Run(context.Background(), Request{Root: root, Target: "."})
	if err == nil || !strings.Contains(err.Error(), "SKILL.md resolves outside repository root") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsUnknownCheck(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := Builtins().Run(context.Background(), Request{Root: root, Target: ".", CheckIDs: []string{"missing"}})
	if err == nil || !strings.Contains(err.Error(), `unknown check "missing"`) {
		t.Fatalf("error = %v", err)
	}
}
