package storyboard

import (
	"sort"
	"strings"
)

func commandDriftItems(
	sb *Storyboard,
	liveRunnableCommandIDs []string,
	liveCommandIDs map[string]bool,
	liveCommandSignatures map[string][]string,
	laneByName map[string]CommandLane,
) []string {
	if len(sb.Commands) == 0 {
		return nil
	}
	if len(laneByName) == 0 && len(sb.Prelude) > 0 && len(liveRunnableCommandIDs) > 0 {
		return []string{"live commands detected; scanner produced a flat application flow instead of per-command lanes"}
	}
	var out []string
	for _, name := range liveRunnableCommandIDs {
		if !laneCoversLiveCommand(name, liveCommandSignatures, laneByName) {
			out = append(out, name+" is registered in Cobra but missing from scan/spec command lanes")
		}
	}
	for _, lane := range sb.CommandLanes {
		if lane.Name == "" || isReportToolLane(lane.Name) {
			continue
		}
		if !liveCommandIDs[lane.Name] {
			out = append(out, lane.Name+" is in scan/spec command lanes but not registered in Cobra")
		}
	}
	for _, diff := range sb.Diffs {
		if diff.Area == "command lanes" {
			out = append(out, diff.Detail)
		}
	}
	sort.Strings(out)
	return out
}

func laneCoversLiveCommand(name string, liveCommandSignatures map[string][]string, laneByName map[string]CommandLane) bool {
	if _, ok := laneByName[name]; ok {
		return true
	}
	parts := strings.Split(name, "_")
	for i := len(parts) - 1; i > 0; i-- {
		parent := strings.Join(parts[:i], "_")
		if _, ok := laneByName[parent]; ok {
			return true
		}
	}
	childPrefix := name + "_"
	for laneName := range laneByName {
		if strings.HasPrefix(laneName, childPrefix) {
			return true
		}
		if commandSignaturePeerCovered(name, laneName, liveCommandSignatures, laneByName) {
			return true
		}
	}
	return false
}

func commandSignaturePeerCovered(name, laneName string, liveCommandSignatures map[string][]string, laneByName map[string]CommandLane) bool {
	if _, ok := laneByName[laneName]; !ok {
		return false
	}
	for _, ids := range liveCommandSignatures {
		hasName := false
		hasLaneName := false
		for _, id := range ids {
			if id == name {
				hasName = true
			}
			if id == laneName {
				hasLaneName = true
			}
		}
		if hasName && hasLaneName {
			return true
		}
	}
	return false
}

func commandLaneByName(lanes []CommandLane) map[string]CommandLane {
	out := make(map[string]CommandLane, len(lanes))
	for _, lane := range lanes {
		out[lane.Name] = lane
	}
	return out
}

func liveRunnableCommandLaneIDs(commands []Command) []string {
	seen := map[string]bool{}
	var out []string
	for _, command := range commands {
		if !command.Runnable {
			continue
		}
		id := commandPathLaneID(command.Path)
		if id == "" || isMetadataCommandLane(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func liveCommandLaneIDs(commands []Command) map[string]bool {
	out := map[string]bool{}
	for _, command := range commands {
		id := commandPathLaneID(command.Path)
		if id == "" || isMetadataCommandLane(id) {
			continue
		}
		out[id] = true
		if alias := commandPathShortLeafLaneID(command.Path); alias != "" && !isMetadataCommandLane(alias) {
			out[alias] = true
		}
	}
	return out
}

func liveCommandSignatureIndex(commands []Command) map[string][]string {
	out := map[string][]string{}
	for _, command := range commands {
		if !command.Runnable || command.Signature == "" {
			continue
		}
		id := commandPathLaneID(command.Path)
		if id == "" || isMetadataCommandLane(id) {
			continue
		}
		out[command.Signature] = append(out[command.Signature], id)
	}
	return out
}

func commandPathLaneID(path string) string {
	fields := strings.Fields(path)
	if len(fields) < 2 {
		return ""
	}
	fields = fields[1:]
	for i, field := range fields {
		fields[i] = strings.ReplaceAll(field, "-", "_")
	}
	return strings.Join(fields, "_")
}

func commandPathShortLeafLaneID(path string) string {
	fields := strings.Fields(path)
	if len(fields) < 2 {
		return ""
	}
	leaf := fields[len(fields)-1]
	head, _, ok := strings.Cut(leaf, "-")
	if !ok || head == "" {
		return ""
	}
	fields = append(append([]string(nil), fields[1:len(fields)-1]...), head)
	return strings.Join(fields, "_")
}

func isMetadataCommandLane(name string) bool {
	switch name {
	case "", commandHelpHelp, commandHelpCompletion, "__complete", "__completeNoDesc":
		return true
	default:
		return false
	}
}

func isReportToolLane(name string) bool {
	switch name {
	case laneScan, "spec", roleRender, "review", commandStoryboard, "validate":
		return true
	default:
		return false
	}
}
