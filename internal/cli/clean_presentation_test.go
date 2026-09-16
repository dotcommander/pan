package cli

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dotcommander/pan/internal/clean"
)

func TestProjectCleanPlanCompactPreservesCandidatesAndReducesSize(t *testing.T) {
	t.Parallel()
	original := largeCleanPlan()
	evidenceBytes := marshalCleanPlan(t, projectCleanPlan(original, detailEvidence))
	compactBytes := marshalCleanPlan(t, projectCleanPlan(original, detailCompact))
	assertCleanPlanReduction(t, evidenceBytes, compactBytes)
	assertCompactCleanPlan(t, original, compactBytes)
}

func largeCleanPlan() clean.Plan {
	files := make([]clean.LabeledFile, 0, 1003)
	for i := 0; i < 1000; i++ {
		files = append(files, clean.LabeledFile{
			File:   fmt.Sprintf("src/pkg%d/file%d.go", i/10, i),
			Status: "clean",
			SizeKB: 10,
		})
	}
	candidates := []clean.Candidate{
		{File: "tmp/scratch1.tmp", Reason: "temporary file", SizeKB: 1},
		{File: "tmp/scratch2.tmp", Reason: "temporary file", SizeKB: 2},
		{File: "tmp/scratch3.tmp", Reason: "temporary file", SizeKB: 3},
	}
	for _, candidate := range candidates {
		files = append(files, clean.LabeledFile{File: candidate.File, Status: "delete", Reason: candidate.Reason, SizeKB: candidate.SizeKB})
	}
	return clean.Plan{
		Schema:           clean.PlanSchema,
		Path:             "/repo",
		HealthScore:      95,
		GitAvailable:     true,
		Note:             "note survives",
		DeleteCandidates: candidates,
		AllFiles:         files,
		Summary:          map[string]int{"total": 3, "delete_candidates": 3},
	}
}

func marshalCleanPlan(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCleanPlanReduction(t *testing.T, evidence, compact []byte) {
	t.Helper()
	reduction := float64(len(evidence)-len(compact)) / float64(len(evidence))
	if reduction < 0.95 {
		t.Fatalf("size reduction = %.2f%%, want >= 95%% (evidence=%d compact=%d)", reduction*100, len(evidence), len(compact))
	}
}

func assertCompactCleanPlan(t *testing.T, original clean.Plan, compact []byte) {
	t.Helper()
	if len(original.AllFiles) != 1003 {
		t.Fatalf("original plan was mutated: len(AllFiles) = %d, want 1003", len(original.AllFiles))
	}
	var decoded struct {
		clean.Plan
		Detail        string             `json:"detail"`
		OmittedFields []string           `json:"omitted_fields"`
		Inventory     CleanPlanInventory `json:"inventory"`
	}
	if err := json.Unmarshal(compact, &decoded); err != nil {
		t.Fatal(err)
	}
	assertCompactCleanPlanFields(t, decoded)
}

func assertCompactCleanPlanFields(t *testing.T, decoded struct {
	clean.Plan
	Detail        string             `json:"detail"`
	OmittedFields []string           `json:"omitted_fields"`
	Inventory     CleanPlanInventory `json:"inventory"`
}) {
	t.Helper()
	if decoded.Detail != "compact" {
		t.Fatalf("detail = %q, want compact", decoded.Detail)
	}
	if len(decoded.OmittedFields) != 1 || decoded.OmittedFields[0] != "all_files" {
		t.Fatalf("omitted_fields = %#v, want ['all_files']", decoded.OmittedFields)
	}
	if decoded.Inventory.Labeled != 1003 || decoded.Inventory.Clean != 1000 || decoded.Inventory.OmittedLabeled != 1003 {
		t.Fatalf("inventory = %#v, want labeled: 1003, clean: 1000, omitted_labeled: 1003", decoded.Inventory)
	}
	if len(decoded.AllFiles) != 0 {
		t.Fatalf("compact result retained all_files: len=%d", len(decoded.AllFiles))
	}
	if len(decoded.DeleteCandidates) != 3 {
		t.Fatalf("compact result lost candidates: len=%d, want 3", len(decoded.DeleteCandidates))
	}
	if decoded.HealthScore != 95 || !decoded.GitAvailable || decoded.Note != "note survives" {
		t.Fatalf("metadata mismatch in compact plan: %+v", decoded.Plan)
	}
}

func TestValidateCleanDetail(t *testing.T) {
	t.Parallel()

	if err := validateCleanDetail(""); err != nil {
		t.Fatalf("empty detail should be valid (defaults to compact), got err = %v", err)
	}
	if err := validateCleanDetail(detailCompact); err != nil {
		t.Fatalf("compact should be valid, got err = %v", err)
	}
	if err := validateCleanDetail(detailEvidence); err != nil {
		t.Fatalf("evidence should be valid, got err = %v", err)
	}
	if err := validateCleanDetail(detailPaths); err == nil {
		t.Fatal("paths detail should be rejected for clean plan")
	}
	if err := validateCleanDetail("invalid"); err == nil {
		t.Fatal("invalid detail should be rejected")
	}
}
