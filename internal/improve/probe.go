package improve

import (
	"context"
	"errors"
	"fmt"
)

// ProbeSchema stamps every probe report.
const ProbeSchema = "pan.improve-probe/v1"

const probeProposalOnly = "proposal-only; no files are applied"

const traceModeOff = "off"

// ProbeOptions configures one proposal-only probe.
type ProbeOptions struct {
	RepoPath string
	Exclude  []string
	Proposer Proposer
	Trace    string
}

// ProbeReport is the result of one proposal-only probe:
// build the dead-code proposal in memory, validate it against the safety
// contract, and measure its line diff against the untouched target. No
// file is written, no test is run, and no history record is appended.
type ProbeReport struct {
	Schema           string         `json:"schema"`
	RunType          string         `json:"run_type"`
	RepoPath         string         `json:"repo_path"`
	Success          bool           `json:"success"`
	Outcome          string         `json:"outcome"`
	Reason           string         `json:"reason"`
	DryRun           bool           `json:"dry_run"`
	Applied          bool           `json:"applied"`
	LiveApply        string         `json:"live_apply"`
	Trace            string         `json:"trace"`
	TraceReceipt     *ProviderTrace `json:"trace_receipt,omitempty"`
	Proposal         *Proposal      `json:"proposal,omitempty"`
	LinesAdded       int            `json:"lines_added,omitempty"`
	LinesDeleted     int            `json:"lines_deleted,omitempty"`
	NetLineReduction int            `json:"net_line_reduction,omitempty"`
}

// RunProbe executes one proposal-only probe: deterministic proposal,
// pre-apply validation, and a measured diff. A validation rejection is a
// completed probe (outcome validation), not an error; only detection and
// measurement failures surface as errors.
func RunProbe(opts ProbeOptions) (ProbeReport, error) {
	return RunProbeContext(context.Background(), opts)
}

// RunProbeContext performs the provider proposal lane when Proposer is set;
// otherwise it preserves Pan's deterministic local proposal behavior. Neither
// lane applies files, runs tests, or opens a Git transaction.
func RunProbeContext(ctx context.Context, opts ProbeOptions) (ProbeReport, error) {
	proposal, trace, err := proposeForProbe(ctx, opts)
	if err != nil {
		return ProbeReport{}, err
	}
	report := ProbeReport{
		Schema:       ProbeSchema,
		RunType:      "probe",
		RepoPath:     opts.RepoPath,
		DryRun:       true,
		LiveApply:    probeProposalOnly,
		Trace:        opts.Trace,
		TraceReceipt: trace,
		Proposal:     proposal,
	}
	if len(proposal.Changes) == 0 {
		if proposal.Changes == nil {
			proposal.Changes = []FileChange{}
		}
		report.Success = true
		report.Outcome = string(OutcomeNoCandidate)
		proposal.Disposition = string(OutcomeNoCandidate)
		report.Reason = proposal.Rationale
		return report, nil
	}
	if err := ValidateProposal(opts.RepoPath, proposal.Changes); err != nil {
		report.Outcome = string(OutcomeValidation)
		report.Reason = fmt.Sprintf("proposal rejected: %v", err)
		return report, nil
	}
	report.LinesAdded, report.LinesDeleted = measureProposal(opts.RepoPath, proposal.Changes)
	report.NetLineReduction = report.LinesDeleted - report.LinesAdded
	report.Success = true
	report.Outcome = string(OutcomeSuccess)
	report.Reason = fmt.Sprintf("proposal validated; %d file(s) ready for the guarded dry-run (nothing was applied)", len(proposal.Changes))
	return report, nil
}

func proposeForProbe(ctx context.Context, opts ProbeOptions) (*Proposal, *ProviderTrace, error) {
	if opts.Proposer == nil {
		proposal, err := ProposeDeadCode(opts.RepoPath, opts.Exclude)
		if err != nil {
			return nil, nil, fmt.Errorf("deterministic proposal: %w", err)
		}
		return proposal, nil, nil
	}
	summary := fmt.Sprintf("Repository root: %s. Return a minimal whole-file refactor proposal. Do not modify tests, configuration, or files outside the repository.", opts.RepoPath)
	repository, packet, repositoryAware, err := scopeRepositoryProposer(ctx, opts.Proposer, opts.RepoPath, opts.Exclude, opts.Trace)
	if repositoryAware {
		if err != nil {
			return nil, nil, err
		}
		proposal, err := repository.ProposeRepository(ctx, summary, packet)
		if err != nil {
			return nil, nil, err
		}
		if proposal == nil {
			return nil, nil, errors.New("repository proposer returned nil proposal")
		}
		return proposal, proposal.TraceReceipt, nil
	}
	var proposal *Proposal
	if opts.Trace != traceModeOff {
		if tracer, ok := opts.Proposer.(interface {
			ProposeWithTrace(context.Context, string) (*Proposal, ProviderTrace, error)
		}); ok {
			proposal, trace, err := tracer.ProposeWithTrace(ctx, summary)
			return proposal, &trace, err
		}
	}
	proposal, err = opts.Proposer.Propose(ctx, summary)
	if err != nil {
		return nil, nil, fmt.Errorf("provider proposal: %w", err)
	}
	return proposal, nil, nil
}
