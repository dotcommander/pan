package auditpacket

import (
	"sort"
	"strconv"
	"strings"
)

const (
	severityLow  = "low"
	severityInfo = "info"
)

func buildLeads(p Packet, opts Options, root string) []Lead {
	var leads []Lead
	mapRepro := "pan flow storyboard " + quoteArgument(root)
	if opts.CommandHelpExecuted {
		leads = append(leads, Lead{
			ID:       "command-help-execution",
			Title:    "storyboard executed target code (go run) to collect command help",
			Severity: "high",
			Evidence: "internal/pipeline/storyboard/commandhelp.go: runProjectCommandHelp runs `go run <target> --help`",
			Repro:    mapRepro + " --command-help=execute",
		})
	}
	if p.Binary.VCSModified {
		leads = append(leads, Lead{
			ID:       "binary-dirty",
			Title:    "running binary was built from a modified working tree",
			Severity: severityLow,
			Evidence: "vcs.modified=true, revision " + shortRev(p.Binary.VCSRevision),
			Repro:    "pan version --json",
		})
	}
	if p.Binary.VCSRevision == "" {
		leads = append(leads, Lead{
			ID:       "binary-unstamped",
			Title:    "binary has no VCS stamp; source provenance cannot be confirmed",
			Severity: severityInfo,
			Evidence: "debug.ReadBuildInfo returned no vcs.revision (go run or VCS-less build)",
			Repro:    "pan version --json",
		})
	}
	if n := len(p.Coverage.Missing); n > 0 {
		leads = append(leads, Lead{
			ID:       "under-modeled-files",
			Title:    strconv.Itoa(n) + " source files are under-modeled in the map",
			Severity: severityLow,
			Evidence: strings.Join(firstN(p.Coverage.Missing, 3), ", "),
			Repro:    mapRepro,
		})
	}
	for i, d := range p.Drift {
		if i >= 10 {
			break
		}
		leads = append(leads, Lead{
			ID:       "command-drift",
			Title:    "drift: " + d.Area + " (" + d.Status + ")",
			Severity: driftSeverity(d.Status),
			Evidence: d.Detail,
			Repro:    "pan flow review " + quoteArgument(p.Target.Project),
		})
	}
	if len(p.Writes) > 0 {
		leads = append(leads, Lead{
			ID:       "persistent-writes",
			Title:    strconv.Itoa(len(p.Writes)) + " persistent write targets detected",
			Severity: severityInfo,
			Evidence: "see writes[]",
			Repro:    mapRepro,
		})
	}
	leads = append(leads, Lead{
		ID:       "generated-artifact",
		Title:    "this packet is a generated artifact; regenerate to verify",
		Severity: severityInfo,
		Evidence: "offline, deterministic; schema " + Schema,
		Repro:    mapRepro + " --audit-json",
	})
	return leads
}

func quoteArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func shortRev(rev string) string {
	if rev == "" {
		return "(none)"
	}
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func driftSeverity(status string) string {
	switch strings.ToLower(status) {
	case "missing", "removed", "absent":
		return "medium"
	case "added", "changed", "modified":
		return severityLow
	default:
		return severityInfo
	}
}

func collectRepro(leads []Lead) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range leads {
		if l.Repro == "" || seen[l.Repro] {
			continue
		}
		seen[l.Repro] = true
		out = append(out, l.Repro)
	}
	sort.Strings(out)
	return out
}
