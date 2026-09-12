// Package improve implements pan's guarded improvement workflows, mined
// from the Pan improvement refactoring agent as the parity source of truth and
// adapted to Pan's guarded contract: deterministic or injected provider
// proposals, explicit gates, and mutation confined to an isolated temporary
// copy of the target repository.
package improve

import "strings"

// RunType distinguishes history record kinds. Refactor and prep runs are
// recorded; read-only commands never write history.
const (
	RunTypeRefactor = "refactor"
	RunTypePrep     = "prep"
)

// Outcome is the canonical gate or terminal state that ended a guarded
// improve run. Outcome strings are persisted to history and run packets;
// treat renames as data migrations, not refactors.
type Outcome string

const (
	// OutcomeSuccess means every gate passed and the measured diff was
	// reported (never committed).
	OutcomeSuccess Outcome = "success"
	// OutcomePreflight is a refusal before any target work: dirty or
	// missing git work tree, or no commits to snapshot.
	OutcomePreflight Outcome = "preflight"
	// OutcomeBaselineTests is a red or unrunnable baseline suite; breakage
	// cannot be attributed to a proposal unless the baseline was green.
	OutcomeBaselineTests Outcome = "baseline_tests"
	// OutcomeBaselineCoverage refuses a refactor until prep has established
	// the configured statement-coverage floor.
	OutcomeBaselineCoverage Outcome = "baseline_coverage"
	// OutcomeProposal is retained for legacy history rows and proposal-loop
	// failures that do not establish a scoped no-candidate result.
	OutcomeProposal Outcome = "proposal"
	// OutcomeNoCandidate means the proposer completed its bounded review and
	// found no justified change within the stated scope and limitations.
	OutcomeNoCandidate Outcome = "no_candidate"
	// OutcomeValidation is a pre-apply proposal safety rejection.
	OutcomeValidation Outcome = "validation"
	// OutcomeApply is a failure writing proposal contents into the
	// isolated copy.
	OutcomeApply Outcome = "apply"
	// OutcomeStaticChecks is a go vet failure on the changed packages in
	// the isolated copy.
	OutcomeStaticChecks Outcome = "static_checks"
	// OutcomePostTests is a red post-change suite in the isolated copy.
	OutcomePostTests Outcome = "post_tests"
	// OutcomeTestIdentity is the anti-cheat gate: the proposal removed or
	// hid baseline tests.
	OutcomeTestIdentity Outcome = "test_identity"
	// OutcomeTestSkipped is the anti-cheat gate: a baseline-active test
	// became skipped.
	OutcomeTestSkipped Outcome = "test_skipped"
	// OutcomeDiff is a diff measurement failure in the isolated copy.
	OutcomeDiff Outcome = "diff"
	// OutcomeCommit is a failure committing the isolated successful proposal.
	OutcomeCommit Outcome = "commit"
	// OutcomeCoverage is a coverage gate failure in the isolated copy.
	OutcomeCoverage Outcome = "coverage"
	// OutcomeMutation is a mutation-score gate failure in the isolated copy.
	OutcomeMutation Outcome = "mutation"
	// OutcomeAlreadySufficient is a prep run whose baseline already meets
	// the coverage floor; the target was left untouched.
	OutcomeAlreadySufficient Outcome = "already_sufficient"
	// OutcomePlanned is a prep run that produced a ranked test-prep plan
	// without generating or applying any files.
	OutcomePlanned Outcome = "planned"
)

// FileChange is one proposed whole-file replacement. Empty NewContents
// means "delete this file"; the deterministic proposer never emits
// deletions today, but the write path keeps the Pan improvement semantics.
type FileChange struct {
	FilePath    string `json:"file_path"`
	NewContents string `json:"new_contents"`
	Reasoning   string `json:"reasoning,omitempty"`
}

// Proposal is a deterministic set of whole-file replacements plus the
// rationale and candidate evidence behind them.
type Proposal struct {
	Rationale   string       `json:"rationale"`
	Changes     []FileChange `json:"changes"`
	Disposition string       `json:"disposition,omitempty"`
	Checked     []string     `json:"checked,omitempty"`
	Limitations []string     `json:"limitations,omitempty"`
	// Audit is the deterministic repository-map evidence supplied to a
	// provider and retained with the resulting proposal.
	Audit *AuditContext `json:"audit,omitempty"`
	// Candidates carries the dead-symbol evidence behind each change.
	Candidates      []DeadSymbol     `json:"candidates,omitempty"`
	CandidatePacket *CandidatePacket `json:"candidate_packet,omitempty"`
	TraceReceipt    *ProviderTrace   `json:"trace_receipt,omitempty"`
	Source          string           `json:"source"`
}

// TestResult is the outcome of one target test-suite run. A test failure
// is AllPassed=false, not an error; only context cancellation and process
// failures surface as errors.
type TestResult struct {
	AllPassed       bool
	TotalTests      int
	Tests           []string // sorted "package\tTest" identities
	Skipped         []string // sorted subset of Tests that emitted skip events
	Failures        []string
	Output          string  // bounded raw go test output for forensics
	TotalCoverage   float64 // statement-weighted percent 0..100
	CoveredStmts    int
	TotalStmts      int
	ProfileMeasured bool
	Files           []FileCoverage // per non-test source file, sorted by path
}

// LineCount counts content lines: newline-terminated segments plus a
// trailing unterminated segment.
func LineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
