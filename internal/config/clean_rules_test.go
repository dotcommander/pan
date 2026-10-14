package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCleanProtectedNamesDefaultsAndInheritance(t *testing.T) {
	t.Parallel()
	filename := filepath.Join(t.TempDir(), "config.yaml")
	// An existing configuration predating protected_names must retain its
	// customized root-document policy and inherit the new protection.
	data := []byte("clean:\n  allowed_root_md: [README.md]\n")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadReadOnlyAt(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Clean.ProtectedNames, "SKILL.md") {
		t.Fatalf("inherited protected names = %v", cfg.Clean.ProtectedNames)
	}
	if !slices.Equal(cfg.Clean.AllowedRootMD, []string{"README.md"}) {
		t.Fatalf("configured root documents changed: %v", cfg.Clean.AllowedRootMD)
	}
	got, err := os.ReadFile(filename)
	if err != nil || !slices.Equal(got, data) {
		t.Fatalf("existing configuration changed: %q, %v", got, err)
	}
	rules := (CleanRules{}).Normalized()
	rules.ProtectedNames[0] = "changed"
	if !slices.Contains(Default().Clean.ProtectedNames, "SKILL.md") {
		t.Fatal("normalization aliased embedded defaults")
	}
}

func TestCleanProtectedNamesRejectPaths(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", ".", "..", "skills/SKILL.md", `skills\SKILL.md`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rules := (CleanRules{ProtectedNames: []string{name}}).Normalized()
			if err := rules.validate(); err == nil || !strings.Contains(err.Error(), "clean.protected_names") {
				t.Fatalf("invalid basename %q: %v", name, err)
			}
		})
	}
}

func TestCleanProtectedNamesCustomPolicy(t *testing.T) {
	t.Parallel()
	rules := (CleanRules{ProtectedNames: []string{"keep.tmp"}}).Normalized()
	if !slices.Equal(rules.ProtectedNames, []string{"keep.tmp"}) {
		t.Fatalf("custom protected names = %v", rules.ProtectedNames)
	}
}
