package review

import (
	"fmt"
	"strings"
)

// SummarySchema identifies the compact deterministic report projection.
const SummarySchema = "pan.review-summary/v1"

// Summary is a privacy-minimal report format for triage and evaluation.
type Summary struct {
	Schema     string         `json:"schema"`
	ReadQueue  int            `json:"read_queue"`
	LaneCounts map[string]int `json:"lane_counts"`
	Top        []Rationale    `json:"top"`
}

// NewSummary projects the report into a compact deterministic format.
func NewSummary(report Report) Summary {
	counts := make(map[string]int)
	for _, item := range report.ReadQueue {
		counts[item.Lane]++
	}
	top := report.Rationale
	if len(top) == 0 {
		top = rationale(report.ReadQueue, min(len(report.ReadQueue), 5))
	}
	return Summary{Schema: SummarySchema, ReadQueue: len(report.ReadQueue), LaneCounts: counts, Top: top}
}

// RenderSummary renders the compact report as deterministic Markdown.
func RenderSummary(root string, report Report) string {
	summary := NewSummary(report)
	var b strings.Builder
	fmt.Fprintf(&b, "# Pan Review Summary\n\n- Repository: `%s`\n- Read queue: `%d`\n\n", cleanSummaryCell(root), summary.ReadQueue)
	if len(summary.Top) == 0 {
		b.WriteString("No rows scored above the review threshold.\n")
		return b.String()
	}
	b.WriteString("| rank | file | score | lane | why |\n| ---: | --- | ---: | --- | --- |\n")
	for _, item := range summary.Top {
		fmt.Fprintf(&b, "| %d | `%s` | %d | %s | %s |\n", item.Rank, cleanSummaryCell(item.Path), item.Score, item.Lane, cleanSummaryCell(strings.Join(item.Why, ", ")))
	}
	return b.String()
}

func cleanSummaryCell(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}
