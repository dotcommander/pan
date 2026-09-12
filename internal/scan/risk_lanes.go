package scan

import "slices"

func buildRiskLanes(laneFiles map[string][]string) []RiskLane {
	names := make([]string, 0, len(laneFiles))
	for name := range laneFiles {
		names = append(names, name)
	}
	slices.Sort(names)
	lanes := make([]RiskLane, 0, len(names))
	for _, name := range names {
		lanes = append(lanes, RiskLane{
			Name:   name,
			Reason: riskLaneReason(name),
			Files:  dedupeAndSort(laneFiles[name]),
		})
	}
	return lanes
}

func riskLaneReason(name string) string {
	switch name {
	case laneAPIContracts:
		return "network or schema-facing code needs contract checks"
	case "architecture":
		return "central files have broad blast radius"
	case laneBestPractices:
		return "change markers and hygiene signals deserve a sweep"
	case "cli-ux":
		return "command entrypoints and user-visible behavior need smoke checks"
	case "coupling":
		return "high fan-out files need package-boundary checks"
	case laneDataIntegrity:
		return "database, filesystem, or persistence boundaries need correctness checks"
	case laneErrorHandling:
		return "subprocess or failure boundaries need actionable errors and cleanup checks"
	case "large-functions":
		return "dense files are harder to review and change safely"
	case laneLifecycleConcurrency:
		return "goroutines and cancellation boundaries need lifecycle checks"
	case lanePerformance:
		return "unbounded reads need resource-bound checks"
	case laneSecurity:
		return "security-sensitive boundaries need explicit review before promotion"
	default:
		return "deterministic risk signal"
	}
}
