package impact

import (
	"fmt"
	"strings"
)

// Text formats the impact Result as a readable terminal tree.
func (r *Result) Text() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "IMPACT BLAST RADIUS: %s\n", r.Target)
	fmt.Fprintf(&builder, "SEVERITY: %s\n", strings.ToUpper(r.Severity))
	fmt.Fprintf(&builder, "SUMMARY:  %s\n\n", r.Summary)
	writeMatches(&builder, r.Matches)
	writeDirectPhases(&builder, r.DirectPhases)
	writeDownstreamPhases(&builder, r.DownstreamPhases)
	writeStrings(&builder, "AFFECTED STORES & STATE", r.AffectedStores, "└─ none")
	writeStrings(&builder, "AFFECTED COMMAND LANES", r.AffectedCommands, "└─ none")
	return builder.String()
}

func writeMatches(builder *strings.Builder, matches []Match) {
	builder.WriteString("MATCHES\n")
	if len(matches) == 0 {
		builder.WriteString("└─ (no direct spec symbols or files matched)\n\n")
		return
	}
	for index, match := range matches {
		detail := ""
		if match.Detail != "" {
			detail = " (" + match.Detail + ")"
		}
		fmt.Fprintf(builder, "%s[%s] %s%s\n", treeBranch(index == len(matches)-1), match.Type, match.Name, detail)
	}
	builder.WriteString("\n")
}

func writeDirectPhases(builder *strings.Builder, phases []AffectedPhase) {
	builder.WriteString("DIRECTLY AFFECTED PHASES\n")
	if len(phases) == 0 {
		builder.WriteString("└─ none\n\n")
		return
	}
	for index, phase := range phases {
		last := index == len(phases)-1
		writePhase(builder, phase, last)
		if len(phase.Stages) > 0 {
			fmt.Fprintf(builder, "%s└─ stages: %s\n", treeChildPrefix(last), strings.Join(phase.Stages, ", "))
		}
	}
	builder.WriteString("\n")
}

func writeDownstreamPhases(builder *strings.Builder, phases []AffectedPhase) {
	builder.WriteString("DOWNSTREAM PROPAGATION PHASES\n")
	if len(phases) == 0 {
		builder.WriteString("└─ none (changes do not cascade downstream)\n\n")
		return
	}
	for index, phase := range phases {
		writePhase(builder, phase, index == len(phases)-1)
	}
	builder.WriteString("\n")
}

func writePhase(builder *strings.Builder, phase AffectedPhase, last bool) {
	command := ""
	if phase.Command != "" {
		command = fmt.Sprintf(" [%s]", phase.Command)
	}
	kind := ""
	if phase.Kind != "" {
		kind = fmt.Sprintf(" (%s)", phase.Kind)
	}
	fmt.Fprintf(builder, "%s%d. %s%s%s\n", treeBranch(last), phase.Ordinal, phase.Name, command, kind)
}

func writeStrings(builder *strings.Builder, heading string, values []string, empty string) {
	builder.WriteString(heading + "\n")
	if len(values) == 0 {
		builder.WriteString(empty + "\n")
		return
	}
	for index, value := range values {
		fmt.Fprintf(builder, "%s%s\n", treeBranch(index == len(values)-1), value)
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
