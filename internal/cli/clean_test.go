package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestCleanPlanDetailProjections(t *testing.T) {
	t.Parallel()

	repo := basicGoRepo()

	// 1. Default (compact): omits all_files, includes detail: compact, omitted_fields: ["all_files"], inventory
	compactOut := runJSON(t, []string{"--repo", repo, "--format", "json", "clean", "plan"})
	if !strings.Contains(compactOut, `"detail": "compact"`) {
		t.Fatalf("compact output missing detail compact:\n%s", compactOut)
	}
	if !strings.Contains(compactOut, `"omitted_fields"`) {
		t.Fatalf("compact output missing omitted_fields:\n%s", compactOut)
	}
	if !strings.Contains(compactOut, `"inventory"`) {
		t.Fatalf("compact output missing inventory:\n%s", compactOut)
	}
	if strings.Contains(compactOut, `"all_files":`) {
		t.Fatalf("compact output unexpectedly contains all_files field:\n%s", compactOut)
	}

	var compactEnvelope struct {
		Result struct {
			Detail        string   `json:"detail"`
			OmittedFields []string `json:"omitted_fields"`
			Inventory     struct {
				Labeled int `json:"labeled"`
			} `json:"inventory"`
			AllFiles []any `json:"all_files"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(compactOut), &compactEnvelope); err != nil {
		t.Fatalf("unmarshal compact output: %v", err)
	}
	if compactEnvelope.Result.Detail != "compact" {
		t.Fatalf("result.detail = %q, want compact", compactEnvelope.Result.Detail)
	}
	if len(compactEnvelope.Result.AllFiles) != 0 {
		t.Fatalf("compact all_files len = %d, want 0", len(compactEnvelope.Result.AllFiles))
	}
	if compactEnvelope.Result.Inventory.Labeled == 0 {
		t.Fatal("compact inventory.labeled should be > 0")
	}

	// 2. Explicit evidence: preserves all_files
	evidenceOut := runJSON(t, []string{"--repo", repo, "--format", "json", "clean", "plan", "--detail", "evidence"})
	if !strings.Contains(evidenceOut, `"all_files"`) {
		t.Fatalf("evidence output missing all_files:\n%s", evidenceOut)
	}

	var evidenceEnvelope struct {
		Result struct {
			AllFiles []any `json:"all_files"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(evidenceOut), &evidenceEnvelope); err != nil {
		t.Fatalf("unmarshal evidence output: %v", err)
	}
	if len(evidenceEnvelope.Result.AllFiles) == 0 {
		t.Fatal("evidence all_files should not be empty")
	}

	// 3. Size reduction on basic repo
	if len(compactOut) >= len(evidenceOut) {
		t.Fatalf("compact output (%d bytes) should be smaller than evidence (%d bytes)", len(compactOut), len(evidenceOut))
	}
}

func TestCleanPlanDetailRejectsInvalid(t *testing.T) {
	t.Parallel()

	repo := basicGoRepo()
	err := cli.Run(context.Background(), []string{"--repo", repo, "--format", "json", "clean", "plan", "--detail", "invalid"}, newTestDeps(nil))
	if err == nil {
		t.Fatal("expected error for invalid --detail, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --detail") {
		t.Fatalf("error = %q, want invalid --detail message", err.Error())
	}
}
