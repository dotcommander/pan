package storyboard

import (
	"fmt"
	"strings"
)

const maxCoverageMissingFiles = 5
const maxStageLabelLen = 112

// RenderText returns the compact terminal storyboard.
func RenderText(sb Storyboard) []byte {
	return renderFullText(sb)
}

func renderFullText(sb Storyboard) []byte {
	var b strings.Builder
	writeHeader(&b, sb)

	writePhaseSection(&b, "SHARED PRELUDE", sb.Prelude)
	b.WriteString("\n")

	pipeline := pipelineForStoryboard(sb)
	if pipeline != nil {
		b.WriteString("SHARED PIPELINES\n")
		fmt.Fprintf(&b, "└─ [Pipeline: %s]\n", pipeline.Name)
		writePhases(&b, "   ", pipeline.Phases)
		b.WriteString("\n")
	}

	b.WriteString("COMMAND LANES\n")
	if len(sb.CommandLanes) == 0 {
		b.WriteString("└─ no command lanes detected\n")
	} else {
		for i, lane := range sb.CommandLanes {
			last := i == len(sb.CommandLanes)-1
			label := lane.Name
			if lane.Description != "" {
				label += " - " + lane.Description
			}
			fmt.Fprintf(&b, "%s%s\n", treeBranch(last), label)
			writeLanePhases(&b, treeChildPrefix(last), lane, pipeline)
		}
	}
	b.WriteString("\n")

	writeStores(&b, sb.Stores)
	b.WriteString("\n")

	writeCoverage(&b, sb.Coverage)
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func writeHeader(b *strings.Builder, sb Storyboard) {
	fmt.Fprintf(b, "STORYBOARD: %s\n", sb.ProjectName)
	if sb.Entrypoint != "" {
		fmt.Fprintf(b, "ENTRYPOINT: %s\n", sb.Entrypoint)
	}
	if sb.ScanWarning != "" {
		fmt.Fprintf(b, "WARNING: %s\n", sb.ScanWarning)
	}
	b.WriteString("\n")
}

func writePhaseSection(b *strings.Builder, title string, phases []Phase) {
	b.WriteString(title + "\n")
	writePhases(b, "", phases)
}

func writePhases(b *strings.Builder, prefix string, phases []Phase) {
	writePhasesFrom(b, prefix, phases, 1)
}

func writePhasesFrom(b *strings.Builder, prefix string, phases []Phase, startOrdinal int) {
	if len(phases) == 0 {
		fmt.Fprintf(b, "%s└─ none\n", prefix)
		return
	}
	for i, phase := range phases {
		last := i == len(phases)-1
		label := phase.Name
		if phase.Kind != "" {
			label += " [" + phase.Kind + "]"
		}
		if phase.Goal != "" {
			label += " - " + phase.Goal
		}
		if phase.TruncatedCount > 0 {
			label += formatTruncatedStages(phase.TruncatedCount)
		}
		fmt.Fprintf(b, "%s%s%d. %s\n", prefix, treeBranch(last), startOrdinal+i, label)
		childPrefix := prefix + treeChildPrefix(last)
		writeStageItems(b, childPrefix, phase.Stages, len(phase.Files) == 0)
		if len(phase.Files) > 0 {
			writeFileItems(b, childPrefix, phase.Files)
		}
	}
}

func writeLanePhases(b *strings.Builder, prefix string, lane CommandLane, pipeline *sharedPipeline) {
	if pipeline == nil {
		writePhases(b, prefix, lane.Phases)
		return
	}
	sharedCount := pipeline.Lanes[lane.Name]
	if sharedCount == 0 {
		writePhases(b, prefix, lane.Phases)
		return
	}

	remaining := lane.Phases[sharedCount:]
	fmt.Fprintf(b, "%s%s1. [Pipeline: %s]\n", prefix, treeBranch(len(remaining) == 0), pipeline.Name)
	if len(remaining) > 0 {
		writePhasesFrom(b, prefix, remaining, 2)
	}
}

func writeFocusedLanePhases(b *strings.Builder, prefix string, lane CommandLane, pipeline *sharedPipeline) {
	if pipeline == nil || pipeline.Lanes[lane.Name] == 0 {
		writePhases(b, prefix, lane.Phases)
		return
	}

	sharedCount := pipeline.Lanes[lane.Name]
	remaining := lane.Phases[sharedCount:]
	pipelineLast := len(remaining) == 0
	fmt.Fprintf(b, "%s%s1. [Pipeline: %s]\n", prefix, treeBranch(pipelineLast), pipeline.Name)
	writePhases(b, prefix+treeChildPrefix(pipelineLast), pipeline.Phases)
	if len(remaining) > 0 {
		writePhasesFrom(b, prefix, remaining, 2)
	}
}

func writeStageItems(b *strings.Builder, prefix string, stages []Stage, finalGroup bool) {
	if len(stages) == 0 {
		if finalGroup {
			fmt.Fprintf(b, "%s└─ stages: none\n", prefix)
		}
		return
	}
	fmt.Fprintf(b, "%s%sstages\n", prefix, treeBranch(finalGroup))
	itemPrefix := prefix + treeChildPrefix(finalGroup)
	for i, stage := range stages {
		stageLast := i == len(stages)-1
		fmt.Fprintf(b, "%s%s%s\n", itemPrefix, treeBranch(stageLast), stageLabel(stage))
	}
}

func stageLabel(stage Stage) string {
	label := compactStageLabel(stage.Label, stage.CodeSnippet)
	if stage.Role != "" {
		label = stage.Role + ": " + label
	}
	if stage.SourceFile == "" {
		return truncateDisplay(label, maxStageLabelLen)
	}
	label = strings.TrimRight(label, " :")
	label = truncateDisplay(label, maxStageLabelLen)
	label += " [" + trimDisplayPath(stage.SourceFile)
	if stage.SourceLine > 0 {
		label += fmt.Sprintf(":%d", stage.SourceLine)
	}
	return label + "]"
}

func writeFileItems(b *strings.Builder, prefix string, files []string) {
	if len(files) <= 3 {
		fmt.Fprintf(b, "%s└─ files: %s\n", prefix, formatFileListWithRoles(files))
		return
	}
	fmt.Fprintf(b, "%s└─ files\n", prefix)
	itemPrefix := prefix + treeChildPrefix(true)
	for i, file := range files {
		fmt.Fprintf(b, "%s%s%s\n", itemPrefix, treeBranch(i == len(files)-1), formatFileWithRole(file))
	}
}

func writeStores(b *strings.Builder, stores []Store) {
	b.WriteString("STORES / STATE\n")
	if len(stores) == 0 {
		b.WriteString("└─ no stores detected by scan\n")
		return
	}
	for i, store := range stores {
		last := i == len(stores)-1
		fmt.Fprintf(b, "%s%s\n", treeBranch(last), store.Name)
		prefix := treeChildPrefix(last)
		if len(store.Writers) == 0 {
			fmt.Fprintf(b, "%s└─ no writers detected\n", prefix)
			continue
		}
		relations := storeRelations(store.Writers)
		for ri, relation := range relations {
			fmt.Fprintf(b, "%s%s%s\n", prefix, treeBranch(ri == len(relations)-1), relation)
		}
	}
}

func storeRelations(writers []Writer) []string {
	reads := make([]string, 0, len(writers))
	writes := make([]string, 0, len(writers))
	uses := make([]string, 0, len(writers))
	for _, writer := range writers {
		label := writer.Stage
		if writer.Note != "" {
			label += " - " + writer.Note
		}
		switch normalizedAccess(writer.Access) {
		case accessRead:
			reads = append(reads, label)
		case accessWrite:
			writes = append(writes, label)
		default:
			uses = append(uses, label)
		}
	}

	out := make([]string, 0, 3)
	if len(reads) > 0 {
		out = append(out, "read by: "+strings.Join(reads, ", "))
	}
	if len(writes) > 0 {
		out = append(out, "written by: "+strings.Join(writes, ", "))
	}
	if len(uses) > 0 {
		out = append(out, "used by: "+strings.Join(uses, ", "))
	}
	return out
}

func normalizedAccess(access string) string {
	switch strings.ToLower(strings.TrimSpace(access)) {
	case "r", accessRead:
		return accessRead
	case "w", accessWrite:
		return accessWrite
	default:
		return ""
	}
}

func writeCoverage(b *strings.Builder, coverage *Coverage) {
	b.WriteString("COVERAGE\n")
	if coverage == nil || coverage.Total == 0 {
		b.WriteString("└─ unavailable\n")
		return
	}
	label := fmt.Sprintf("%d/%d source files represented", coverage.Represented, coverage.Total)
	if coverage.Warning != "" {
		label += " - " + coverage.Warning
	}
	fmt.Fprintf(b, "└─ %s\n", label)
	if len(coverage.Missing) == 0 {
		return
	}
	limit := len(coverage.Missing)
	if limit > maxCoverageMissingFiles {
		limit = maxCoverageMissingFiles
	}
	truncated := len(coverage.Missing) > limit
	for i := 0; i < limit; i++ {
		last := i == limit-1 && !truncated
		fmt.Fprintf(b, "   %s%s\n", treeBranch(last), trimDisplayPath(coverage.Missing[i]))
	}
	if truncated {
		fmt.Fprintf(b, "   └─ ... %d more\n", len(coverage.Missing)-limit)
	}
}

func treeBranch(last bool) string {
	if last {
		return "└─ "
	}
	return "├─ "
}

func treeChildPrefix(last bool) string {
	if last {
		return "   "
	}
	return "│  "
}

func formatTruncatedStages(count int) string {
	switch count {
	case 0:
		return ""
	case 1:
		return " (+1 stage truncated)"
	default:
		return fmt.Sprintf(" (+%d stages truncated)", count)
	}
}
