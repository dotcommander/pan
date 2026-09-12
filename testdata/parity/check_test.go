package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV2ReceiptBindsEveryRequiredCase(t *testing.T) {
	root := writeV2Fixture(t, true)
	if err := check(root); err != nil {
		t.Fatalf("check() error = %v", err)
	}
}

func TestV2ReceiptRejectsMissingClaim(t *testing.T) {
	root := writeV2Fixture(t, false)
	err := check(root)
	if err == nil || !strings.Contains(err.Error(), "does not contain every manifest claim") {
		t.Fatalf("check() error = %v, want missing claim", err)
	}
}

func TestV2ReceiptRejectsClaimBoundToWrongProbe(t *testing.T) {
	root := writeV2Fixture(t, true)
	path := filepath.Join(root, "testdata/parity/receipts/v2.json")
	r, err := readJSON[receipt](path)
	if err != nil {
		t.Fatal(err)
	}
	r.Claims[0].Probe = "enabled"
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	err = check(root)
	if err == nil || !strings.Contains(err.Error(), "owning manifest case") {
		t.Fatalf("check() error = %v, want wrong-probe rejection", err)
	}
}

func TestSourceFailurePanSuccessAssertion(t *testing.T) {
	if err := checkAssertion(probe{SourceExitCode: 1, PanExitCode: 0, Assertion: "source-failure-pan-success"}); err != nil {
		t.Fatal(err)
	}
	if err := checkAssertion(probe{SourceExitCode: 0, PanExitCode: 0, Assertion: "source-failure-pan-success"}); err == nil {
		t.Fatal("expected zero source exit to be rejected")
	}
}

func TestSourceFailurePanSuccessAllowsSilentSourceOutput(t *testing.T) {
	p := probe{Assertion: "source-failure-pan-success", PanOutput: "Pan completed"}
	if !probeOutputsComplete(p) {
		t.Fatal("silent source output was rejected")
	}
	p.PanOutput = ""
	if probeOutputsComplete(p) {
		t.Fatal("missing Pan output was accepted")
	}
}

func TestPairedExitSuccessAllowsEmptyOutput(t *testing.T) {
	root := writeV2Fixture(t, true)
	manifestPath := filepath.Join(root, "testdata/parity/manifests/fixture.json")
	m, err := readJSON[manifest](manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Cases[0].Assertion = "paired-exit-success"
	manifestData, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(manifestData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestHash, err := fileSHA256(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(root, "testdata/parity/receipts/v2.json")
	r, err := readJSON[receipt](receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	r.ManifestSHA256 = manifestHash
	r.Probes[0].Assertion = "paired-exit-success"
	r.Probes[0].SourceOutput = ""
	r.Probes[0].PanOutput = ""
	receiptData, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, append(receiptData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := check(root); err != nil {
		t.Fatal(err)
	}
}

func TestPairedExitFailureAllowsEmptyOutput(t *testing.T) {
	p := probe{Assertion: "paired-exit-failure", SourceExitCode: 1, PanExitCode: 2}
	if !probeOutputsComplete(p) {
		t.Fatal("empty failure output was rejected")
	}
	if err := checkAssertion(p); err != nil {
		t.Fatal(err)
	}
	p.PanExitCode = 0
	if err := checkAssertion(p); err == nil {
		t.Fatal("successful Pan exit was accepted")
	}
}

func TestV2ReceiptRejectsHelpOnlyClaim(t *testing.T) {
	for _, helpArg := range []string{"--help", "--help=true", "-h", "-help=true", "help"} {
		t.Run(helpArg, func(t *testing.T) {
			root := writeV2Fixture(t, true)
			path := filepath.Join(root, "testdata/parity/receipts/v2.json")
			r, err := readJSON[receipt](path)
			if err != nil {
				t.Fatal(err)
			}
			r.Probes[0].SourceArgs = []string{"run", helpArg}
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			err = check(root)
			if err == nil || !strings.Contains(err.Error(), "help-only probe") {
				t.Fatalf("check() error = %v, want help-only probe rejection", err)
			}
		})
	}
}

func writeV2Fixture(t *testing.T, includeSecondClaim bool) string {
	t.Helper()
	root := t.TempDir()
	write := func(path string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmdFile := filepath.Join(root, "cmd", "main.go")
	if err := os.MkdirAll(filepath.Dir(cmdFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmdFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "internal", "x.go"), filepath.Join(root, ".work", "source", "x.go"), filepath.Join(root, "testdata", "fixture", "x.txt"), filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "testdata/parity/commands.json"), commandMap{Schema: commandSchema, Commands: []command{{Source: "source run", Pan: "context run", Flags: []string{"flag"}}}})
	write(filepath.Join(root, "testdata/parity/input-coverage.json"), coverage{Schema: coverageSchema, Seed: "commands.json", Status: "test", Commands: []coverageCommand{{Source: "source run", Pan: "context run", Inputs: []input{{Name: "--flag", RequiredCases: []string{"default", "enabled"}, Status: "verified_bounded", Evidence: "receipts/v2.json"}}}}})
	claims := []claim{{Source: "source run", Pan: "context run", Input: "--flag", RequiredCase: "default"}, {Source: "source run", Pan: "context run", Input: "--flag", RequiredCase: "enabled"}}
	m := manifest{Schema: manifestSchema, Contract: "fixture/v2", SnapshotRoots: []string{"cmd", "internal", "go.mod", "go.sum", "testdata/parity/commands.json", ".work/source", "testdata/fixture"}, Cases: []manifestCase{{ID: "default", Assertion: "paired-success", Claims: []claim{claims[0]}}, {ID: "enabled", Assertion: "paired-success", Claims: []claim{claims[1]}}}}
	manifestPath := filepath.Join(root, "testdata/parity/manifests/fixture.json")
	write(manifestPath, m)
	manifestHash, err := fileSHA256(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshot(root, []string{"cmd", "internal", "go.mod", "go.sum", "testdata/parity/commands.json", ".work/source", "testdata/fixture"})
	if err != nil {
		t.Fatal(err)
	}
	binaryHash, err := fileSHA256("/bin/echo")
	if err != nil {
		t.Fatal(err)
	}
	receiptClaims := []claim{{Source: "source run", Pan: "context run", Input: "--flag", RequiredCase: "default", Probe: "default"}}
	if includeSecondClaim {
		receiptClaims = append(receiptClaims, claim{Source: "source run", Pan: "context run", Input: "--flag", RequiredCase: "enabled", Probe: "enabled"})
	}
	write(filepath.Join(root, "testdata/parity/receipts/v2.json"), receipt{Schema: receiptSchemaV2, Contract: "fixture/v2", Manifest: "manifests/fixture.json", ManifestSHA256: manifestHash, SnapshotRoots: []string{"cmd", "internal", "go.mod", "go.sum", "testdata/parity/commands.json", ".work/source", "testdata/fixture"}, Snapshot: snapshot, BinarySHA256: map[string]string{"/bin/echo": binaryHash}, Probes: []probe{{Name: "default", SourceArgs: []string{"run"}, PanArgs: []string{"run"}, SourceOutput: "ok", PanOutput: "ok", Assertion: "paired-success"}, {Name: "enabled", SourceArgs: []string{"run", "--flag"}, PanArgs: []string{"run", "--flag"}, SourceOutput: "ok", PanOutput: "ok", Assertion: "paired-success"}}, Claims: receiptClaims})
	return root
}
