package storyboard

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/review"
	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// EnrichReview populates sb.ReviewFindings by running the review engine against
// the comparison spec at reviewSpecPath. No-op when reviewSpecPath is empty or
// no ranked files were supplied.
func EnrichReview(ctx context.Context, sb *Storyboard, root string, ranked []symbols.RankedFile, reviewSpecPath string) {
	sb.ReviewFindings = reviewFindings(ctx, root, ranked, reviewSpecPath)
}

// EnrichDiff populates sb.Diffs by comparing the live scan against the spec at
// comparePath. No-op when comparePath is empty.
func EnrichDiff(sb *Storyboard, scanned *spec.Spec, comparePath string) {
	sb.Diffs = diffSpec(scanned, comparePath)
}

// EnrichCommandDrift populates sb.CommandDrift from the harvested command
// surface (sb.Commands) versus the scanned command lanes. Run after EnrichDiff
// so command-lane diffs feed the drift list.
func EnrichCommandDrift(sb *Storyboard) {
	if len(sb.Commands) == 0 {
		sb.CommandDrift = nil
		return
	}
	liveCommandIDs := liveCommandLaneIDs(sb.Commands)
	liveRunnableCommandIDs := liveRunnableCommandLaneIDs(sb.Commands)
	liveCommandSignatures := liveCommandSignatureIndex(sb.Commands)
	laneByName := commandLaneByName(sb.CommandLanes)
	sb.CommandDrift = commandDriftItems(sb, liveRunnableCommandIDs, liveCommandIDs, liveCommandSignatures, laneByName)
}

func reviewFindings(ctx context.Context, root string, ranked []symbols.RankedFile, specPath string) []ReviewFinding {
	if specPath == "" || len(ranked) == 0 {
		return nil
	}
	s, err := spec.Load(specPath)
	if err != nil {
		return []ReviewFinding{{
			Severity: severityHigh,
			Check:    "review-load",
			Message:  err.Error(),
		}}
	}
	report := review.Review(ctx, s, root, ranked)
	out := make([]ReviewFinding, 0, len(report.Findings))
	var coverageGaps []string
	for _, finding := range report.Findings {
		if finding.Check == "coverage-gap" {
			path := strings.TrimPrefix(finding.Message, "file ")
			path = strings.TrimSuffix(path, " not referenced by any phase")
			coverageGaps = append(coverageGaps, path)
			continue
		}
		out = append(out, ReviewFinding{
			Severity: finding.Severity.String(),
			Check:    finding.Check,
			Phase:    finding.Phase,
			Message:  finding.Message,
			Count:    1,
		})
	}
	if len(coverageGaps) > 0 {
		sort.Strings(coverageGaps)
		evidence := append([]string(nil), coverageGaps...)
		if len(evidence) > 30 {
			evidence = append(evidence[:30], fmt.Sprintf("... %d more", len(coverageGaps)-30))
		}
		out = append(out, ReviewFinding{
			Severity: "medium",
			Check:    "coverage-gap",
			Message:  fmt.Sprintf("%d Go source files are not referenced by the reviewed spec. Use Diff Mode and Source View to decide whether this is scan drift or intentional omission.", len(coverageGaps)),
			Count:    len(coverageGaps),
			Evidence: evidence,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		wi := severityWeight(out[i].Severity)
		wj := severityWeight(out[j].Severity)
		if wi != wj {
			return wi > wj
		}
		if out[i].Check != out[j].Check {
			return out[i].Check < out[j].Check
		}
		return out[i].Message < out[j].Message
	})
	return out
}
