package auditpacket

import (
	"fmt"
	"strings"
)

// RenderMarkdown renders the packet as a human-facing audit brief.
func RenderMarkdown(p Packet) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Audit packet — %s\n\n", p.Target.Project)
	fmt.Fprintf(&b, "- schema: `%s`\n", p.Schema)
	fmt.Fprintf(&b, "- root: `%s`\n", p.Target.Root)
	if p.Target.Module != "" {
		fmt.Fprintf(&b, "- module: `%s`\n", p.Target.Module)
	}
	if p.Target.GitRevision != "" {
		dirty := "clean"
		if p.Target.GitDirty != nil && *p.Target.GitDirty {
			dirty = "dirty"
		}
		fmt.Fprintf(&b, "- git: `%s` (%s)\n", shortRev(p.Target.GitRevision), dirty)
	}
	fmt.Fprintf(&b, "- binary: go `%s`, module `%s`, rev `%s`, dirty `%t`\n",
		dash(p.Binary.GoVersion), dash(p.Binary.ModuleVersion), shortRev(p.Binary.VCSRevision), p.Binary.VCSModified)
	fmt.Fprintf(&b, "- coverage: %d/%d represented, %d under-modeled\n\n",
		p.Coverage.Represented, p.Coverage.Total, len(p.Coverage.Missing))

	b.WriteString("## Audit Leads\n\n")
	if len(p.FindingsLeads) == 0 {
		b.WriteString("_No leads._\n\n")
	} else {
		b.WriteString("| severity | lead | evidence | repro |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, l := range p.FindingsLeads {
			fmt.Fprintf(&b, "| %s | %s | %s | `%s` |\n",
				l.Severity, mdCell(l.Title), mdCell(l.Evidence), l.Repro)
		}
		b.WriteString("\n")
	}

	if len(p.Subprocesses) > 0 {
		b.WriteString("## Subprocesses\n\n")
		for _, s := range p.Subprocesses {
			fmt.Fprintf(&b, "- `%s` (source: %s)\n", s.Command, s.Source)
		}
		b.WriteString("\n")
	}
	if len(p.Drift) > 0 {
		b.WriteString("## Drift\n\n")
		for _, d := range p.Drift {
			fmt.Fprintf(&b, "- **%s** %s — %s\n", d.Area, d.Status, d.Detail)
		}
		b.WriteString("\n")
	}
	if len(p.ReproCommands) > 0 {
		b.WriteString("## Repro commands\n\n")
		for _, r := range p.ReproCommands {
			fmt.Fprintf(&b, "- `%s`\n", r)
		}
		b.WriteString("\n")
	}
	if len(p.Limitations) > 0 {
		b.WriteString("## Limitations\n\n")
		for _, lim := range p.Limitations {
			fmt.Fprintf(&b, "- %s\n", lim)
		}
		b.WriteString("\n")
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
