package provideracceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOfflineValidatesThreeFixtureContracts(t *testing.T) {
	o := testOptions(t)
	o.Execute = fixtureExecutor(t, "", false)
	receipt, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "passed" || len(receipt.Cases) != 3 {
		t.Fatalf("status/cases = %q/%d, want passed/3", receipt.Status, len(receipt.Cases))
	}
	for _, c := range receipt.Cases {
		if c.Status != "passed" || c.FinalClassification != c.ExpectedClassification {
			t.Fatalf("case %q = status %q classification %q/%q", c.Name, c.Status, c.FinalClassification, c.ExpectedClassification)
		}
		if c.ResponseCount != 0 || c.ExplorationCallCount != 0 || c.AcceptedTerminalCount != 0 {
			t.Fatalf("offline case %q recorded provider activity", c.Name)
		}
	}
	entries, err := os.ReadDir(o.ReceiptDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("receipt files = %d, err = %v", len(entries), err)
	}
	if err := VerifyReceipt(context.Background(), filepath.Join(o.ReceiptDir, entries[0].Name()), o); err != nil {
		t.Fatalf("verify fresh receipt: %v", err)
	}
}

func TestRunRejectsClassificationMismatch(t *testing.T) {
	o := testOptions(t)
	o.Execute = fixtureExecutor(t, "none", false)
	receipt, err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), `classification "success", want "no_candidate"`) {
		t.Fatalf("error = %v, want classification mismatch", err)
	}
	if receipt.Status != "failed" {
		t.Fatalf("status = %q, want failed", receipt.Status)
	}
}

func TestRunRejectsUnexpectedEnvelopeSchema(t *testing.T) {
	o := testOptions(t)
	o.Execute = func(context.Context, string, []string) ([]byte, []byte, error) {
		return []byte(`{"schema_version":"other/v1","analysis":{"complete":true},"result":{}}`), nil, nil
	}
	_, err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "unexpected pan envelope") {
		t.Fatalf("error = %v, want envelope rejection", err)
	}
}

func TestRunRejectsFixtureMutation(t *testing.T) {
	o := testOptions(t)
	o.Execute = fixtureExecutor(t, "", true)
	_, err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "fixture bytes changed") {
		t.Fatalf("error = %v, want fixture mutation rejection", err)
	}
}

func TestRunKeepsInvalidOutputOutOfReceiptJSON(t *testing.T) {
	o := testOptions(t)
	o.Execute = func(context.Context, string, []string) ([]byte, []byte, error) {
		return []byte("not-json"), []byte("api_key=super-secret"), nil
	}
	receipt, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("expected invalid JSON rejection")
	}
	if len(receipt.Cases) == 0 || receipt.Cases[0].Result != nil {
		t.Fatal("invalid stdout must not be embedded as json.RawMessage")
	}
	if strings.Contains(receipt.Cases[0].Stderr, "super-secret") || !strings.Contains(receipt.Cases[0].Stderr, "[REDACTED]") {
		t.Fatalf("stderr was not sanitized: %q", receipt.Cases[0].Stderr)
	}
	entries, readErr := os.ReadDir(o.ReceiptDir)
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("failed-run receipt files = %d, err = %v", len(entries), readErr)
	}
}

func TestRemovedGoSymbolsDerivesProviderDeletionIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	mustWrite(t, path, "package fixture\n\nfunc Used() {}\nfunc unusedHelper() {}\n", 0o600)
	removed, err := removedGoSymbols(path, []byte("package fixture\n\nfunc Used() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(removed, ","); got != "unusedHelper" {
		t.Fatalf("removed symbols = %q, want unusedHelper", got)
	}
}

func TestParseProbeDerivesSymbolsWhenProviderOmitsCandidates(t *testing.T) {
	fixture := t.TempDir()
	mustWrite(t, filepath.Join(fixture, "main.go"), "package fixture\n\nfunc Used() {}\nfunc unusedHelper() {}\n", 0o600)
	result := map[string]any{
		"schema": "pan.improve-probe/v1", "success": true, "outcome": "success", "dry_run": true, "applied": false,
		"proposal":      map[string]any{"changes": []map[string]string{{"file_path": "main.go", "new_contents": "package fixture\n\nfunc Used() {}\n"}}},
		"trace_receipt": map[string]any{"model": "fixture-model", "response_count": 1, "accepted_terminal_count": 1, "prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 19},
	}
	data, err := json.Marshal(map[string]any{"schema_version": "pan/v1", "analysis": map[string]any{"complete": true}, "result": result})
	if err != nil {
		t.Fatal(err)
	}
	c := Case{Name: "obvious", ExpectedClassification: "success"}
	if err := parseProbe(data, fixture, &c, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.AffectedSymbols, ","); got != "unusedHelper" {
		t.Fatalf("affected symbols = %q, want unusedHelper", got)
	}
	if c.Tokens != 19 {
		t.Fatalf("tokens = %d, want provider total 19", c.Tokens)
	}
}

func TestVerifyReceiptRejectsChangedConfig(t *testing.T) {
	o := testOptions(t)
	o.Execute = fixtureExecutor(t, "", false)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(o.ReceiptDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("receipt files = %d, err = %v", len(entries), err)
	}
	if err := os.WriteFile(o.Config, []byte("provider: gemini\nmodel: changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = VerifyReceipt(context.Background(), filepath.Join(o.ReceiptDir, entries[0].Name()), o)
	if err == nil || !strings.Contains(err.Error(), "config mismatch") {
		t.Fatalf("error = %v, want stale config rejection", err)
	}
}

func testOptions(t *testing.T) Options {
	t.Helper()
	d := t.TempDir()
	bin, cfg, fixtures, repo := filepath.Join(d, "pan"), filepath.Join(d, "pan-improve.yaml"), filepath.Join(d, "fixtures"), filepath.Join(d, "repo")
	mustWrite(t, bin, "binary", 0o755)
	mustWrite(t, cfg, "provider: gemini\nmodel: fixture-model\n", 0o600)
	for _, name := range fixtureNames {
		mustWrite(t, filepath.Join(fixtures, name, "input.go"), "package "+name+"\n", 0o600)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	mustWrite(t, filepath.Join(repo, "tracked.txt"), "tracked\n", 0o600)
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "-c", "user.name=Pan Test", "-c", "user.email=pan@example.invalid", "commit", "-qm", "fixture")
	return Options{Binary: bin, Config: cfg, Fixtures: fixtures, ReceiptDir: filepath.Join(d, "receipts"), Repo: repo}
}

func fixtureExecutor(t *testing.T, mismatch string, mutate bool) Executor {
	t.Helper()
	return func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
		fixture := argumentAfter(args, "--repo")
		name := filepath.Base(fixture)
		if mutate {
			mustWrite(t, filepath.Join(fixture, "mutated.txt"), "changed\n", 0o600)
		}
		outcome, checked := "no_candidate", []string{"input.go"}
		var files, symbols []string
		if name == "obvious" || name == mismatch {
			outcome, files, symbols = "success", []string{"main.go"}, []string{"unusedHelper"}
		}
		result := map[string]any{"schema": "pan.improve-probe/v1", "success": true, "outcome": outcome, "dry_run": true, "applied": false, "proposal": map[string]any{"checked": checked, "limitations": []string{"lexical fixture"}, "changes": changes(files), "candidates": candidates(symbols)}}
		data, err := json.Marshal(map[string]any{"schema_version": "pan/v1", "analysis": map[string]any{"complete": true}, "result": result})
		return data, nil, err
	}
}

func changes(files []string) []map[string]string {
	result := make([]map[string]string, 0, len(files))
	for _, file := range files {
		result = append(result, map[string]string{"file_path": file})
	}
	return result
}
func candidates(symbols []string) []map[string]string {
	result := make([]map[string]string, 0, len(symbols))
	for _, symbol := range symbols {
		result = append(result, map[string]string{"name": symbol})
	}
	return result
}
func argumentAfter(args []string, flag string) string {
	for i := range args {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func mustWrite(t *testing.T, path, value string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatal(err)
	}
}
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, output))
	}
}
