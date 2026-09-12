package eval

import (
	"fmt"
	"strings"
)

// RenderMarkdown renders the evaluation result as privacy-minimal Markdown:
// aggregate fields only, never reattaching paths, identities, or input
// filenames to the outcome ledger.
func RenderMarkdown(result Result) string {
	var b strings.Builder
	fmt.Fprint(&b, "# Pan Evaluation\n\n")
	fmt.Fprintf(&b, "- Reports: `%d`; rows: `%d`\n", result.Reports, result.Rows)
	fmt.Fprintf(&b, "- Outcomes: confirmed `%d`, refuted `%d`, unknown `%d`, skipped `%d`\n", result.Outcomes.Confirmed, result.Outcomes.Refuted, result.Outcomes.Unknown, result.Outcomes.Skipped)
	fmt.Fprintf(&b, "- Labels: labeled `%d`, found `%d`, missing `%d`; coverage `%.4f`\n", result.Calibration.Labeled, result.Calibration.Found, result.Calibration.Missing, result.Calibration.LabeledCoverage)
	fmt.Fprintf(&b, "- Context cost: files opened `%d`, tool calls `%d`, review ms `%d`\n", result.ContextCost.FilesOpened, result.ContextCost.ToolCalls, result.ContextCost.ReviewMS)
	fmt.Fprintf(&b, "- Top-K: `%d`; slots `%d`; total rows `%d`; distinct scores `%d`; saturation `%.4f`\n", result.Calibration.TopK, result.Calibration.TopKSlots, result.Calibration.TotalRows, result.Calibration.DistinctScoresInTopK, result.Calibration.TopKScoreSaturation)
	fmt.Fprintf(&b, "- Recall: confirmed Top-K `%.4f`; confirmed found `%.4f`; Top-K noise `%.4f`\n", result.Calibration.ConfirmedTopKRecall, result.Calibration.ConfirmedFoundRecall, result.Calibration.NoiseTopKRate)
	fmt.Fprintf(&b, "- Ledger warnings: partial trailing records `%d`; unmatched outcomes `%d`\n", result.Warnings.PartialTrailingRecords, result.Warnings.UnmatchedOutcomes)
	return b.String()
}
