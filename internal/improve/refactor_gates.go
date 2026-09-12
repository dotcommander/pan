package improve

import (
	"context"
	"fmt"
	"os"
)

type settleRunInput struct {
	opts         RefactorOptions
	toolchain    Toolchain
	copyDir      string
	proposal     *Proposal
	baseline     TestResult
	structural   StructuralMetrics
	linesAdded   int
	linesDeleted int
}
type settleRunResult struct {
	post    TestResult
	outcome Outcome
	reason  string
	err     error
}

func changedFiles(proposal *Proposal) []string {
	changed := make([]string, 0, len(proposal.Changes))
	for _, change := range proposal.Changes {
		changed = append(changed, change.FilePath)
	}
	return changed
}

func runSettlement(ctx context.Context, in settleRunInput, report *RefactorReport) settleRunResult {
	post, err := in.toolchain.RunTests(ctx, in.copyDir)
	if err != nil {
		return settleRunResult{err: fmt.Errorf("post-change suite: %w", err)}
	}
	if !post.AllPassed {
		return settleRunResult{post: post, outcome: OutcomePostTests, reason: "tests failed after applying the proposal in the isolated copy"}
	}
	if reason := testIdentityReason(in.baseline, post); reason != "" {
		outcome := OutcomeTestSkipped
		if post.TotalTests < in.baseline.TotalTests || missingBaselineTest(in.baseline, post) {
			outcome = OutcomeTestIdentity
		}
		return settleRunResult{post: post, outcome: outcome, reason: reason}
	}
	if reason := coverageGateReason(in.baseline, post, in.proposal.Changes, in.opts.CoverageGate, in.opts.CoverageDrop); reason != "" {
		return settleRunResult{post: post, outcome: OutcomeCoverage, reason: reason}
	}
	if in.opts.MutationGate {
		if in.opts.MutationRunner == nil {
			return settleRunResult{post: post, outcome: OutcomeMutation, reason: "mutation gate enabled but no mutation runner was configured"}
		}
		score, err := in.opts.MutationRunner.Run(ctx, in.copyDir, ChangedPackages(changedFiles(in.proposal)))
		if err != nil {
			return settleRunResult{post: post, outcome: OutcomeMutation, reason: fmt.Sprintf("mutation gate failed: %v", err)}
		}
		report.MutationScore = score
		if score < in.opts.MutationFloor {
			return settleRunResult{post: post, outcome: OutcomeMutation, reason: fmt.Sprintf("mutation score %.2f%% is below the %.2f%% floor", score*100, in.opts.MutationFloor*100)}
		}
	}
	report.LinesAdded, report.LinesDeleted = in.linesAdded, in.linesDeleted
	report.NetLineReduction = in.linesDeleted - in.linesAdded
	report.SymbolsDeleted = in.structural.SymbolsDeleted
	report.SymbolsAdded = in.structural.SymbolsAdded
	report.SymbolsModified = in.structural.SymbolsModified
	report.NetSymbolReduction = in.structural.NetSymbolReduction
	report.StructuralMetrics = true
	if report.NetLineReduction < 0 {
		return settleRunResult{post: post, outcome: OutcomeDiff, reason: fmt.Sprintf("proposal adds %d net lines", -report.NetLineReduction)}
	}
	return settleRunResult{post: post, outcome: OutcomeSuccess, reason: fmt.Sprintf("all gates passed; %d symbol(s) removed for a %d-line net reduction (dry-run)", report.SymbolsDeleted, report.NetLineReduction)}
}

func coverageGateReason(base, post TestResult, changes []FileChange, enabled bool, tolerance float64) string {
	if !enabled {
		return ""
	}
	byFile := make(map[string]float64, len(post.Files))
	for _, file := range post.Files {
		byFile[file.File] = file.Coverage
	}
	for _, file := range base.Files {
		for _, change := range changes {
			if change.FilePath != file.File || change.NewContents == "" {
				continue
			}
			current, ok := byFile[file.File]
			if !ok {
				return fmt.Sprintf("coverage gate: %s lost all test coverage", file.File)
			}
			if file.Coverage-current > tolerance {
				return fmt.Sprintf("coverage gate: %s dropped %.2f%% to %.2f%% (tolerance %.2f)", file.File, file.Coverage, current, tolerance)
			}
		}
	}
	return ""
}

func measureProposal(root string, changes []FileChange) (added, deleted int) {
	for _, change := range changes {
		old, err := os.ReadFile(joinRoot(root, change.FilePath))
		if err != nil {
			added += LineCount(change.NewContents)
			continue
		}
		a, d := DiffLines(string(old), change.NewContents)
		added, deleted = added+a, deleted+d
	}
	return added, deleted
}
func joinRoot(root, rel string) string {
	if rel == "" {
		return root
	}
	return root + string(os.PathSeparator) + rel
}

func testIdentityReason(baseline, post TestResult) string {
	baseIDs, postIDs := topLevelTestIDs(baseline.Tests), topLevelTestIDs(post.Tests)
	for id := range baseIDs {
		if !postIDs[id] {
			return fmt.Sprintf("safety violation: test %q disappeared after the proposal", testDisplayName(id))
		}
	}
	baseSkipped, postSkipped := topLevelTestIDs(baseline.Skipped), topLevelTestIDs(post.Skipped)
	for id := range postSkipped {
		if baseIDs[id] && !baseSkipped[id] {
			return fmt.Sprintf("safety violation: test %q was skipped after the proposal (active at baseline)", testDisplayName(id))
		}
	}
	return ""
}
func missingBaselineTest(baseline, post TestResult) bool {
	for id := range topLevelTestIDs(baseline.Tests) {
		if !topLevelTestIDs(post.Tests)[id] {
			return true
		}
	}
	return false
}
func topLevelTestIDs(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		pkg, name, ok := cutTab(id)
		for i := 0; i < len(name); i++ {
			if name[i] == '/' {
				name = name[:i]
				break
			}
		}
		if name != "" {
			if ok {
				out[pkg+"\t"+name] = true
			} else {
				out[name] = true
			}
		}
	}
	return out
}
func testDisplayName(id string) string {
	pkg, name, _ := cutTab(id)
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}
func cutTab(s string) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
