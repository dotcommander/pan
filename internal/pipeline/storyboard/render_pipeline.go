package storyboard

import "strings"

const scanEnginePipelineName = "Scan Engine"

type sharedPipeline struct {
	Name   string
	Phases []Phase
	Lanes  map[string]int
}

func annotateSharedPipelines(sb *Storyboard) {
	pipeline := detectScanEnginePipeline(sb.CommandLanes)
	if pipeline == nil {
		return
	}

	lanes := make([]PipelineLane, 0, len(pipeline.Lanes))
	for _, lane := range sb.CommandLanes {
		phaseCount := pipeline.Lanes[lane.Name]
		if phaseCount == 0 {
			continue
		}
		lanes = append(lanes, PipelineLane{Command: lane.Name, PhaseCount: phaseCount})
	}
	sb.SharedPipelines = []Pipeline{{
		Name:   pipeline.Name,
		Phases: append([]Phase(nil), pipeline.Phases...),
		Lanes:  lanes,
	}}

	for i := range sb.CommandLanes {
		phaseCount := pipeline.Lanes[sb.CommandLanes[i].Name]
		if phaseCount == 0 {
			continue
		}
		sb.CommandLanes[i].PipelineRefs = []PipelineRef{{
			Name:       pipeline.Name,
			PhaseCount: phaseCount,
		}}
	}
}

func pipelineForStoryboard(sb Storyboard) *sharedPipeline {
	if len(sb.SharedPipelines) > 0 {
		pipeline := sb.SharedPipelines[0]
		lanes := make(map[string]int, len(pipeline.Lanes))
		for _, lane := range pipeline.Lanes {
			lanes[lane.Command] = lane.PhaseCount
		}
		if len(lanes) > 0 {
			return &sharedPipeline{
				Name:   pipeline.Name,
				Phases: pipeline.Phases,
				Lanes:  lanes,
			}
		}
	}
	return detectScanEnginePipeline(sb.CommandLanes)
}

func detectScanEnginePipeline(lanes []CommandLane) *sharedPipeline {
	var scanLane *CommandLane
	for i := range lanes {
		if lanes[i].Name == laneScan && len(lanes[i].Phases) >= 2 {
			scanLane = &lanes[i]
			break
		}
	}
	if scanLane == nil {
		return nil
	}

	pipelinePhases := scanPipelinePhases(scanLane.Phases)
	if len(pipelinePhases) < 2 {
		return nil
	}

	laneMatches := map[string]int{laneScan: len(scanLane.Phases)}
	for _, lane := range lanes {
		if lane.Name == laneScan {
			continue
		}
		if matched := scanPipelinePrefixLen(lane.Phases, pipelinePhases); matched > 0 {
			laneMatches[lane.Name] = matched
		}
	}
	if len(laneMatches) < 2 {
		return nil
	}
	return &sharedPipeline{
		Name:   scanEnginePipelineName,
		Phases: pipelinePhases,
		Lanes:  laneMatches,
	}
}

func scanPipelinePhases(phases []Phase) []Phase {
	out := make([]Phase, 0, len(phases))
	for _, phase := range phases {
		if phaseHasFilePrefix(phase, "internal/commands/") {
			continue
		}
		if phaseHasFilePrefix(phase, "internal/scan/") {
			out = append(out, phase)
		}
	}
	return out
}

func scanPipelinePrefixLen(phases, pipeline []Phase) int {
	if len(phases) < len(pipeline) {
		return 0
	}
	for i := range pipeline {
		if !phaseMatchesScanPipeline(phases[i], pipeline[i]) {
			return 0
		}
	}
	return len(pipeline)
}

func phaseMatchesScanPipeline(phase, pipeline Phase) bool {
	if phase.Name == pipeline.Name {
		return true
	}
	for _, left := range phase.Files {
		if !strings.HasPrefix(left, "internal/scan/") {
			continue
		}
		for _, right := range pipeline.Files {
			if left == right {
				return true
			}
		}
	}
	return false
}

func phaseHasFilePrefix(phase Phase, prefix string) bool {
	for _, file := range phase.Files {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}
