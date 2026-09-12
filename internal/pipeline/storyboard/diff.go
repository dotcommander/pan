package storyboard

import (
	"sort"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func diffSpec(scanned *spec.Spec, specPath string) []DiffItem {
	if specPath == "" {
		return nil
	}
	if scanned == nil {
		return []DiffItem{{Area: areaScan, Status: statusMissing, Detail: "live scan did not produce a spec"}}
	}
	existing, err := spec.Load(specPath)
	if err != nil {
		return []DiffItem{{Area: areaCompare, Status: "error", Detail: err.Error(), Evidence: []string{specPath}}}
	}
	var out []DiffItem
	out = append(out, diffSet("prelude phases", phaseNames(existing.Phases), phaseNames(scanned.Phases))...)
	out = append(out, diffSet("command lanes", commandNames(existing.Commands), commandNames(scanned.Commands))...)
	out = append(out, diffSet("stores", storeNames(existing.Stores), storeNames(scanned.Stores))...)
	out = append(out, diffSet("represented files", uniquePhaseFiles(existing.AllPhases()), uniquePhaseFiles(scanned.AllPhases()))...)
	if len(out) == 0 {
		out = append(out, DiffItem{
			Area:     areaCompare,
			Status:   "match",
			Detail:   "comparison spec and live scan have matching phase names, command lanes, stores, and represented files",
			Evidence: []string{specPath},
		})
	}
	if len(out) > 40 {
		out = append(out[:40], DiffItem{
			Area:   areaCompare,
			Status: "truncated",
			Detail: "diff output capped at 40 items",
		})
	}
	return out
}

func diffSet(area string, before, after []string) []DiffItem {
	beforeSet := stringSet(before)
	afterSet := stringSet(after)
	var out []DiffItem
	for value := range afterSet {
		if !beforeSet[value] {
			out = append(out, DiffItem{Area: area, Status: "added", Detail: value})
		}
	}
	for value := range beforeSet {
		if !afterSet[value] {
			out = append(out, DiffItem{Area: area, Status: "removed", Detail: value})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Area != out[j].Area {
			return out[i].Area < out[j].Area
		}
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func phaseNames(phases []spec.Phase) []string {
	out := make([]string, 0, len(phases))
	for _, phase := range phases {
		out = append(out, phase.Name)
	}
	sort.Strings(out)
	return out
}

func commandNames(commands []spec.Command) []string {
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		out = append(out, command.Name)
	}
	sort.Strings(out)
	return out
}

func storeNames(stores []spec.Store) []string {
	out := make([]string, 0, len(stores))
	for _, store := range stores {
		out = append(out, store.Name)
	}
	sort.Strings(out)
	return out
}

func uniquePhaseFiles(phases []spec.Phase) []string {
	seen := map[string]bool{}
	for _, p := range phases {
		for _, file := range p.Files {
			seen[file] = true
		}
	}
	out := make([]string, 0, len(seen))
	for file := range seen {
		out = append(out, file)
	}
	sort.Strings(out)
	return out
}

func severityWeight(sev string) int {
	switch sev {
	case "critical":
		return 4
	case severityHigh:
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}
