package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPairedExitSuccessAssertion(t *testing.T) {
	if err := checkAssertion("paired-exit-success", 0, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := checkAssertion("paired-exit-success", 1, 0, "", ""); err == nil {
		t.Fatal("expected nonzero source exit to be rejected")
	}
}

func TestPairedExitFailureAssertion(t *testing.T) {
	if err := checkAssertion("paired-exit-failure", 1, 2, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := checkAssertion("paired-exit-failure", 1, 0, "", ""); err == nil {
		t.Fatal("expected zero Pan exit to be rejected")
	}
}

func TestClaimedHelpInvocation(t *testing.T) {
	for _, args := range [][]string{{"run", "--help"}, {"run", "--help=true"}, {"run", "-h"}, {"run", "-help=true"}, {"help"}} {
		if !claimedHelpInvocation(manifestCase{SourceArgs: args, Claims: []claim{{Input: "--flag"}}}) {
			t.Fatalf("claimedHelpInvocation(%q) = false", args)
		}
	}
	if claimedHelpInvocation(manifestCase{SourceArgs: []string{"run"}, Claims: []claim{{Input: "--flag"}}}) {
		t.Fatal("ordinary invocation identified as help")
	}
	if claimedHelpInvocation(manifestCase{SourceArgs: []string{"run", "--help"}}) {
		t.Fatal("unclaimed help inventory identified as verified behavior")
	}
}

func TestIsolatedEnvironment(t *testing.T) {
	t.Setenv("PARITY_SECRET", "must-not-pass")
	root := filepath.Join(t.TempDir(), "home")
	joined := strings.Join(isolatedEnvironment(root), "\n")
	if strings.Contains(joined, "PARITY_SECRET") {
		t.Fatal("ambient variable passed to comparison process")
	}
	for _, want := range []string{"HOME=" + root, "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_STATE_HOME=" + filepath.Join(root, "state"), "TMPDIR=" + filepath.Join(root, "tmp")} {
		if !strings.Contains(joined, want) {
			t.Fatalf("isolated environment missing %q", want)
		}
	}
}
