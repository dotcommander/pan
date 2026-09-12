package storyboard

import (
	"fmt"
	"strings"
)

const (
	// TextViewFull renders every detected phase with stages and files.
	TextViewFull = "full"
	// TextViewSummary renders the high-density overview for terminal reading.
	TextViewSummary = "summary"
	// TextViewStores renders only persistent stores, access relations, and coverage.
	TextViewStores = "stores"
)

// TextOptions controls terminal storyboard detail level.
type TextOptions struct {
	// View is "full", "summary", "stores", or "command=<name>".
	View string
}

// RenderTextWithOptions returns terminal storyboard output with the requested
// disclosure level.
func RenderTextWithOptions(sb Storyboard, opts TextOptions) []byte {
	view := strings.TrimSpace(opts.View)
	switch {
	case view == "", view == TextViewFull:
		return renderFullText(sb)
	case view == TextViewSummary:
		return renderSummaryText(sb)
	case view == TextViewStores:
		return renderStoresText(sb)
	case strings.HasPrefix(view, "command="):
		return renderCommandText(sb, strings.TrimSpace(strings.TrimPrefix(view, "command=")))
	default:
		return renderFullText(sb)
	}
}

func renderSummaryText(sb Storyboard) []byte {
	var b strings.Builder
	writeHeader(&b, sb)

	writePhaseSummarySection(&b, "SHARED PRELUDE", sb.Prelude)
	b.WriteString("\n")

	pipeline := pipelineForStoryboard(sb)
	if pipeline != nil {
		b.WriteString("SHARED PIPELINES\n")
		fmt.Fprintf(&b, "└─ [Pipeline: %s]\n", pipeline.Name)
		writePhaseSummaries(&b, "   ", pipeline.Phases)
		b.WriteString("\n")
	}

	writeCommandSummary(&b, sb.CommandLanes, pipeline, "")
	b.WriteString("\n")

	writeStores(&b, sb.Stores)
	b.WriteString("\n")

	writeCoverage(&b, sb.Coverage)
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func renderStoresText(sb Storyboard) []byte {
	var b strings.Builder
	writeHeader(&b, sb)

	writeStores(&b, sb.Stores)
	b.WriteString("\n")

	writeCoverage(&b, sb.Coverage)
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func renderCommandText(sb Storyboard, commandName string) []byte {
	var b strings.Builder
	writeHeader(&b, sb)

	pipeline := pipelineForStoryboard(sb)
	writeCommandSummary(&b, sb.CommandLanes, pipeline, commandName)
	b.WriteString("\n")

	lane, ok := findCommandLane(sb.CommandLanes, commandName)
	b.WriteString("COMMAND DETAIL\n")
	switch {
	case commandName == "":
		b.WriteString("└─ missing command name; use --view command=<name>\n")
	case !ok:
		fmt.Fprintf(&b, "└─ command %q not detected\n", commandName)
	default:
		fmt.Fprintf(&b, "└─ %s\n", lane.Name)
		writeFocusedLanePhases(&b, "   ", lane, pipeline)
	}
	b.WriteString("\n")

	writeStores(&b, sb.Stores)
	b.WriteString("\n")

	writeCoverage(&b, sb.Coverage)
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func writePhaseSummarySection(b *strings.Builder, title string, phases []Phase) {
	b.WriteString(title + "\n")
	writePhaseSummaries(b, "", phases)
}

func writePhaseSummaries(b *strings.Builder, prefix string, phases []Phase) {
	if len(phases) == 0 {
		fmt.Fprintf(b, "%s└─ none\n", prefix)
		return
	}
	for i, phase := range phases {
		last := i == len(phases)-1
		label := phaseSummaryLabel(i+1, phase)
		fmt.Fprintf(b, "%s%s%s\n", prefix, treeBranch(last), label)
	}
}

func phaseSummaryLabel(ordinal int, phase Phase) string {
	label := fmt.Sprintf("%d. %s", ordinal, phase.Name)
	if phase.Kind != "" {
		label += " [" + phase.Kind + "]"
	}
	if phase.Goal != "" {
		label += " - " + phase.Goal
	}
	if len(phase.Files) > 0 {
		label += " | " + formatFileSummary(phase.Files)
	}
	if phase.TruncatedCount > 0 {
		label += formatTruncatedStages(phase.TruncatedCount)
	}
	return label
}

func writeCommandSummary(b *strings.Builder, lanes []CommandLane, pipeline *sharedPipeline, selected string) {
	b.WriteString("COMMAND LANES\n")
	if len(lanes) == 0 {
		b.WriteString("└─ no command lanes detected\n")
		return
	}
	for i, lane := range lanes {
		last := i == len(lanes)-1
		label := lane.Name
		if selected != "" && lane.Name == selected {
			label += " *"
		}
		if lane.Description != "" {
			label += " - " + lane.Description
		}
		fmt.Fprintf(b, "%s%s\n", treeBranch(last), label)
		writeLanePhaseSummary(b, treeChildPrefix(last), lane, pipeline)
	}
}

func writeLanePhaseSummary(b *strings.Builder, prefix string, lane CommandLane, pipeline *sharedPipeline) {
	if pipeline == nil || pipeline.Lanes[lane.Name] == 0 {
		writePhaseSummaries(b, prefix, lane.Phases)
		return
	}
	sharedCount := pipeline.Lanes[lane.Name]
	remaining := lane.Phases[sharedCount:]
	fmt.Fprintf(b, "%s%s1. [Pipeline: %s]\n", prefix, treeBranch(len(remaining) == 0), pipeline.Name)
	for i, phase := range remaining {
		last := i == len(remaining)-1
		fmt.Fprintf(b, "%s%s%s\n", prefix, treeBranch(last), phaseSummaryLabel(i+2, phase))
	}
}

func findCommandLane(lanes []CommandLane, name string) (CommandLane, bool) {
	for _, lane := range lanes {
		if lane.Name == name {
			return lane, true
		}
	}
	return CommandLane{}, false
}
