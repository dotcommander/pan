package cli

import (
	"fmt"

	"github.com/dotcommander/pan/internal/clean"
)

func validateCleanDetail(detail detailLevel) error {
	if detail == "" {
		return nil
	}
	if detail != detailCompact && detail != detailEvidence {
		return fmt.Errorf("invalid --detail %q (want compact or evidence)", detail)
	}
	return nil
}

// CleanPlanInventory records the disposition counts from plan.AllFiles.
type CleanPlanInventory struct {
	Labeled        int `json:"labeled"`
	Clean          int `json:"clean"`
	OmittedLabeled int `json:"omitted_labeled"`
}

// CompactCleanPlanResult wraps clean.Plan with compact presentation metadata.
// In compact mode, AllFiles is omitted to reduce output noise and token volume.
type CompactCleanPlanResult struct {
	clean.Plan
	Detail        detailLevel        `json:"detail"`
	OmittedFields []string           `json:"omitted_fields"`
	Inventory     CleanPlanInventory `json:"inventory"`
}

// projectCleanPlan projects clean.Plan into compact (anomaly-first) or evidence mode.
// For compact:
// - Copies the Plan value.
// - Sets only the copy's AllFiles to nil (omitted on serialization).
// - Computes inventory counts from actual labels.
// - Preserves all candidates, summary, health score, and notes without recomputing.
// For evidence:
// - Returns plan directly, preserving the exact exhaustive result shape.
func projectCleanPlan(plan clean.Plan, detail detailLevel) any {
	if detail == "" {
		detail = detailCompact
	}
	if detail == detailEvidence {
		return plan
	}
	labeled := len(plan.AllFiles)
	cleanCount := 0
	for _, f := range plan.AllFiles {
		if f.Status == "clean" {
			cleanCount++
		}
	}
	copyPlan := plan
	copyPlan.AllFiles = nil

	return CompactCleanPlanResult{
		Plan:          copyPlan,
		Detail:        detailCompact,
		OmittedFields: []string{"all_files"},
		Inventory: CleanPlanInventory{
			Labeled:        labeled,
			Clean:          cleanCount,
			OmittedLabeled: labeled,
		},
	}
}
