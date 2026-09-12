package impact

import (
	"fmt"
	"strings"
)

// Markdown returns formatted markdown of the Result.
func (r *Result) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Impact Analysis: `%s`\n\n", r.Target)
	fmt.Fprintf(&b, "**Severity:** `%s`  \n", strings.ToUpper(r.Severity))
	fmt.Fprintf(&b, "**Summary:** %s\n\n", r.Summary)

	b.WriteString("## Direct Phases\n\n")
	if len(r.DirectPhases) == 0 {
		b.WriteString("*(None)*\n\n")
	} else {
		for _, p := range r.DirectPhases {
			cmdStr := ""
			if p.Command != "" {
				cmdStr = fmt.Sprintf(" (`%s`)", p.Command)
			}
			fmt.Fprintf(&b, "- **%s**%s (Kind: `%s`)\n", p.Name, cmdStr, p.Kind)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Downstream Cascade\n\n")
	if len(r.DownstreamPhases) == 0 {
		b.WriteString("*(No cascading impact)*\n\n")
	} else {
		for _, p := range r.DownstreamPhases {
			cmdStr := ""
			if p.Command != "" {
				cmdStr = fmt.Sprintf(" (`%s`)", p.Command)
			}
			fmt.Fprintf(&b, "- **%s**%s\n", p.Name, cmdStr)
		}
		b.WriteString("\n")
	}

	if len(r.AffectedStores) > 0 {
		b.WriteString("## Affected Stores\n\n")
		for _, st := range r.AffectedStores {
			fmt.Fprintf(&b, "- `%s`\n", st)
		}
		b.WriteString("\n")
	}

	if len(r.AffectedCommands) > 0 {
		b.WriteString("## Affected Commands\n\n")
		for _, cmd := range r.AffectedCommands {
			fmt.Fprintf(&b, "- `%s`\n", cmd)
		}
		b.WriteString("\n")
	}

	return b.String()
}
