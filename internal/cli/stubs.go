package cli

import "path/filepath"

// displayRepo resolves the reported repository path without asserting that
// the target exists.
func displayRepo(repo string) string {
	if abs, err := filepath.Abs(repo); err == nil {
		return abs
	}
	return repo
}

// ReviewCmd groups structured audit packets and reports.
type ReviewCmd struct {
	Brief   ReviewBriefCmd   `cmd:"" help:"Bounded audit first-read brief."`
	Report  ReviewReportCmd  `cmd:"" help:"Full audit report."`
	Risks   ReviewRisksCmd   `cmd:"" help:"Risk packets."`
	Effects ReviewEffectsCmd `cmd:"" help:"Effect packets."`
	Eval    ReviewEvalCmd    `cmd:"" help:"Evaluate an outcome ledger against report documents."`
	Outputs ReviewOutputsCmd `cmd:"" help:"Report output inventory."`
}
