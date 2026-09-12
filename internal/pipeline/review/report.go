package review

import (
	"fmt"
	"strings"
)

// titleCase uppercases the first rune of s.
func titleCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Markdown returns the report as a formatted markdown string with findings
// grouped by severity (Critical → High → Medium → Info).
func (r *Report) Markdown() string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Review: %s\n\n", r.SpecPath)

	// Group findings by severity.
	groups := map[Severity][]Finding{}
	for _, f := range r.Findings {
		groups[f.Severity] = append(groups[f.Severity], f)
	}

	// Print severity sections in descending urgency order.
	for _, sev := range []Severity{Critical, High, Medium, Info} {
		fmt.Fprintf(&sb, "## %s\n\n", titleCase(sev.String()))
		findings := groups[sev]
		if len(findings) == 0 {
			sb.WriteString("(none)\n\n")
			continue
		}
		for _, f := range findings {
			phase := ""
			if f.Phase != "" {
				phase = fmt.Sprintf(" [phase %q]", f.Phase)
			}
			fmt.Fprintf(&sb, "- [%s]%s %s\n", f.Check, phase, f.Message)
		}
		sb.WriteString("\n")
	}

	// Summary line.
	counts := map[Severity]int{}
	for _, f := range r.Findings {
		counts[f.Severity]++
	}
	fmt.Fprintf(&sb, "---\n%d finding(s): %d critical, %d high, %d medium, %d info\n",
		len(r.Findings),
		counts[Critical],
		counts[High],
		counts[Medium],
		counts[Info],
	)

	return sb.String()
}
