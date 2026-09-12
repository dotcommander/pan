package review

import (
	"fmt"
	"strings"

	"github.com/dotcommander/pan/internal/scan"
)

// maxMarkdownLaneFiles bounds the file list shown per risk lane; counts
// always reflect the full lane.
const maxMarkdownLaneFiles = 5

// RenderMarkdown renders the composed report as a deterministic Markdown
// document. Output is a pure function of the report: no timestamps and no
// machine-specific paths beyond the repository root.
func RenderMarkdown(root string, report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Pan Review Report\n\n")
	fmt.Fprintf(&b, "- Repository: %s\n", markdownCell(root))
	fmt.Fprintf(&b, "- Scoring: deterministic fallback\n")
	fmt.Fprintf(&b, "- Files: `%d`; symbols: `%d`; edges: `%d`\n", report.Overview.Files, report.Overview.Symbols, report.Overview.Edges)
	fmt.Fprintf(&b, "- Read queue: `%d` rows", len(report.ReadQueue))
	if report.Changes.GitAvailable {
		fmt.Fprintf(&b, "; churn window: `%d` days", report.Changes.Days)
	}
	fmt.Fprint(&b, "\n\n")

	writeReadQueueMarkdown(&b, report.ReadQueue)
	writeRationaleMarkdown(&b, report.Rationale)
	writeLanesMarkdown(&b, report.Risks.Lanes)
	writeHygieneMarkdown(&b, report.Hygiene)
	if report.CullLedger != nil {
		writeCullLedgerMarkdown(&b, *report.CullLedger)
	}
	return b.String()
}

func writeRationaleMarkdown(b *strings.Builder, entries []Rationale) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprint(b, "## Why These Rows\n\n")
	fmt.Fprint(b, "| rank | file | score | evidence |\n| ---: | --- | ---: | --- |\n")
	for _, entry := range entries {
		fmt.Fprintf(b, "| %d | %s | %d | %s |\n", entry.Rank, markdownCell(entry.Path), entry.Score, markdownCell(strings.Join(entry.Why, ", ")))
	}
	b.WriteString("\n")
}

func writeReadQueueMarkdown(b *strings.Builder, queue []ReadItem) {
	fmt.Fprintf(b, "## Read Queue\n\n")
	if len(queue) == 0 {
		fmt.Fprintf(b, "No rows scored above the review threshold.\n\n")
		return
	}
	fmt.Fprintf(b, "| rank | file | score | lane | why |\n")
	fmt.Fprintf(b, "| ---: | --- | ---: | --- | --- |\n")
	for _, item := range queue {
		fmt.Fprintf(b, "| %d | %s | %d | %s | %s |\n",
			item.Rank,
			markdownCell(item.Path),
			item.Score,
			item.Lane,
			markdownCell(strings.Join(item.Why, ", ")),
		)
	}
	b.WriteString("\n")
}

func writeLanesMarkdown(b *strings.Builder, lanes []scan.RiskLane) {
	if len(lanes) == 0 {
		return
	}
	fmt.Fprintf(b, "## Risk Lanes\n\n")
	fmt.Fprintf(b, "| lane | reason | files |\n")
	fmt.Fprintf(b, "| --- | --- | --- |\n")
	for _, lane := range lanes {
		files := make([]string, 0, min(len(lane.Files), maxMarkdownLaneFiles))
		for _, file := range lane.Files {
			if len(files) == maxMarkdownLaneFiles {
				files = append(files, fmt.Sprintf("... (%d more)", len(lane.Files)-maxMarkdownLaneFiles))
				break
			}
			files = append(files, markdownCell(file))
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", markdownCell(lane.Name), lane.Reason, strings.Join(files, " "))
	}
	b.WriteString("\n")
}

func writeHygieneMarkdown(b *strings.Builder, hygiene scan.HygieneReport) {
	if len(hygiene.Issues) == 0 {
		return
	}
	fmt.Fprintf(b, "## Hygiene Issues\n\n")
	fmt.Fprintf(b, "| severity | id | path |\n")
	fmt.Fprintf(b, "| --- | --- | --- |\n")
	for _, issue := range hygiene.Issues {
		fmt.Fprintf(b, "| %s | %s | %s |\n", issue.Severity, markdownCell(issue.ID), markdownCell(issue.Path))
	}
	b.WriteString("\n")
}

func writeCullLedgerMarkdown(b *strings.Builder, ledger CullLedger) {
	fmt.Fprintf(b, "## Cull Ledger\n\n")
	fmt.Fprintf(b, "Deterministic separation of the read queue; production review starts with `%s`.\n\n", LaneKept)
	fmt.Fprintf(b, "| lane | count |\n")
	fmt.Fprintf(b, "| --- | ---: |\n")
	for _, lane := range cullLaneOrder() {
		fmt.Fprintf(b, "| %s | %d |\n", lane, ledger.Counts[lane])
	}
	fmt.Fprintf(b, "\n| rank | file | score | lane | reason |\n")
	fmt.Fprintf(b, "| ---: | --- | ---: | --- | --- |\n")
	for _, entry := range ledger.Entries {
		fmt.Fprintf(b, "| %d | %s | %d | %s | %s |\n",
			entry.Rank, markdownCell(entry.Path), entry.Score, entry.Lane, entry.Reason)
	}
	b.WriteString("\n")
}

// markdownCell renders one table cell as inline code, neutralizing backticks.
func markdownCell(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "'") + "`"
}
