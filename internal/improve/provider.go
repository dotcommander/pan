package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/provider"
)

const (
	providerSummaryRole = "summary"
	providerSuccess     = "success"
	providerSystemRole  = "system"
	providerUserRole    = "user"
	providerToolRole    = "tool"
	providerSource      = "provider"
	providerSubmit      = "submit_proposal"
	providerDeletions   = "submit_deletions"
	providerNoCandidate = "submit_no_candidate"
)

// CompletionClient is the Pan provider boundary used by improve. Keeping this
// interface local makes proposal parsing testable with a fake and prevents the
// guarded workflow from owning transport or credentials.
type CompletionClient interface {
	Complete(context.Context, provider.Request) (provider.Response, error)
}

// Proposer is the proposal boundary shared by the provider and deterministic
// lanes. It permits fake providers in tests without coupling gate code to HTTP.
type Proposer interface {
	Propose(context.Context, string) (*Proposal, error)
}

// RepositoryProposer is the optional source-backed proposal seam. It lets the
// probe and refactor entrypoints share candidate construction and repository
// scoping without coupling that lifecycle to one provider transport.
type RepositoryProposer interface {
	Proposer
	ScopeRepository(root string, exclude []string, trace string) RepositoryProposer
	ProposeRepository(context.Context, string, CandidatePacket) (*Proposal, error)
}

// ProviderProposer requests one whole-file proposal. It does not retry: a
// transport failure after submission has an unknown provider outcome.
type ProviderProposer struct {
	Client   CompletionClient
	Model    string
	Settings ProviderSettings
}

// ProviderTrace is a sanitized receipt for one proposal request. It excludes
// authorization, prompt contents, and completion contents. Full tracing may
// retain scrubbed, bounded read-only tool results.
type ProviderTrace struct {
	Status                string              `json:"status"`
	ElapsedMillis         int64               `json:"elapsed_millis"`
	ResponseID            string              `json:"response_id,omitempty"`
	Model                 string              `json:"model,omitempty"`
	FinishReason          string              `json:"finish_reason,omitempty"`
	PromptTokens          int                 `json:"prompt_tokens,omitempty"`
	CompletionTokens      int                 `json:"completion_tokens,omitempty"`
	TotalTokens           int                 `json:"total_tokens,omitempty"`
	ResponseCount         int                 `json:"response_count"`
	ExplorationCallCount  int                 `json:"exploration_call_count"`
	AcceptedTerminalCount int                 `json:"accepted_terminal_count"`
	TerminalTool          string              `json:"terminal_tool,omitempty"`
	Tools                 []ProviderToolTrace `json:"tools,omitempty"`
}

// WithRepository returns a copy scoped to root. Refactor callers must use the
// isolated worktree root, never the original checkout.
func (p ProviderProposer) WithRepository(root string) ProviderProposer {
	p.Settings.RepoPath = root
	return p
}

// ScopeRepository returns a repository-scoped proposer while preserving the
// concrete WithRepository API for existing callers.
func (p ProviderProposer) ScopeRepository(root string, exclude []string, trace string) RepositoryProposer {
	p = p.WithRepository(root)
	p.Settings.Exclude = append(append([]string(nil), p.Settings.Exclude...), exclude...)
	if trace != "" {
		p.Settings.TraceMode = trace
	}
	return p
}

func scopeRepositoryProposer(ctx context.Context, proposer Proposer, root string, exclude []string, trace string) (RepositoryProposer, CandidatePacket, bool, error) {
	repository, ok := proposer.(RepositoryProposer)
	if !ok {
		return nil, CandidatePacket{}, false, nil
	}
	packet, err := BuildCandidatePacket(ctx, root, exclude)
	if err != nil {
		return nil, CandidatePacket{}, true, err
	}
	return repository.ScopeRepository(root, exclude, trace), packet, true, nil
}

// Propose preserves the legacy summary-only seam. When a repository has been
// explicitly configured it uses the source-backed agentic entry point.
func (p ProviderProposer) Propose(ctx context.Context, summary string) (*Proposal, error) {
	if p.Settings.RepoPath != "" {
		return p.ProposeRepository(ctx, summary, CandidatePacket{})
	}
	proposal, _, err := p.ProposeWithTrace(ctx, summary)
	return proposal, err
}

// ProposeWithTrace makes one request and returns its sanitized transport
// receipt. It does not retry a potentially transmitted request.
func (p ProviderProposer) ProposeWithTrace(ctx context.Context, summary string) (*Proposal, ProviderTrace, error) {
	if p.Client == nil {
		return nil, ProviderTrace{}, errors.New("improve provider proposer requires a client")
	}
	started := time.Now()
	if p.Settings.RepoPath != "" {
		traced := p
		if traced.Settings.TraceMode == "" {
			traced.Settings.TraceMode = providerSummaryRole
		}
		proposal, err := traced.ProposeRepository(ctx, summary, CandidatePacket{})
		trace := ProviderTrace{ElapsedMillis: time.Since(started).Milliseconds(), Model: p.Model}
		if proposal != nil && proposal.TraceReceipt != nil {
			trace = *proposal.TraceReceipt
		}
		if err != nil {
			trace.Status = "error"
			return nil, trace, err
		}
		trace.Status = providerSuccess
		return proposal, trace, nil
	}
	response, err := p.Client.Complete(ctx, provider.Request{Model: p.Model, Messages: []provider.Message{
		{Role: providerSystemRole, Content: "Return one JSON object with rationale and changes. Each change must replace one non-test source file. Do not modify tests."},
		{Role: providerUserRole, Content: summary},
	}})
	if err != nil {
		return nil, ProviderTrace{Status: "error", ElapsedMillis: time.Since(started).Milliseconds()}, fmt.Errorf("request proposal: %w", err)
	}
	trace := ProviderTrace{Status: providerSuccess, ElapsedMillis: time.Since(started).Milliseconds(), ResponseID: response.ID, Model: response.Model, PromptTokens: response.Usage.PromptTokens, CompletionTokens: response.Usage.CompletionTokens, TotalTokens: response.Usage.TotalTokens, ResponseCount: 1}
	if len(response.Choices) != 1 {
		return nil, trace, errors.New("provider proposal requires exactly one choice")
	}
	trace.FinishReason = response.Choices[0].FinishReason
	var proposal Proposal
	if err := json.Unmarshal([]byte(response.Choices[0].Message.Content), &proposal); err != nil {
		return nil, trace, errors.New("provider returned invalid proposal JSON")
	}
	proposal.Source = providerSource
	if strings.TrimSpace(proposal.Rationale) == "" {
		return nil, trace, errors.New("provider proposal is missing rationale")
	}
	return &proposal, trace, nil
}

// ProposeRepository supplies a bounded source snapshot then permits only
// read-only exploration before one terminal proposal submission. It performs
// no filesystem mutation and never retries an uncertain provider request.
func (p ProviderProposer) ProposeRepository(ctx context.Context, summary string, candidates CandidatePacket) (*Proposal, error) {
	if p.Client == nil {
		return nil, errors.New("improve provider proposer requires a client")
	}
	started := time.Now()
	reader, err := newProviderReader(p.Settings.RepoPath, p.Settings.Exclude, p.Settings.resultBytes())
	if err != nil {
		return nil, err
	}
	reader.jinnBin = p.Settings.JinnBin
	source, err := reader.bundle(p.Settings.sourceBytes())
	if err != nil {
		return nil, fmt.Errorf("build provider source bundle: %w", err)
	}
	system := strings.TrimSpace(p.Settings.SystemPrompt)
	if system == "" {
		system = "You are a bounded Pan improvement refactoring agent. Use only the supplied read-only tools. Submit one conservative proposal with evidence when justified, otherwise submit an explicit scoped no-candidate result. Never modify files or manufacture work."
	}
	audit := BuildAuditContext(ctx, p.Settings.RepoPath, p.Settings.Exclude)
	prompt := p.refactorPrompt(summary, candidates, source, audit)
	var correction string
	var receipt ProviderTrace
	for attempt := 0; attempt <= p.Settings.MaxRetries; attempt++ {
		proposal, trace, err := p.proposeRepositoryAttempt(providerAttemptInput{ctx: ctx, reader: reader, system: system, prompt: prompt + correction, candidates: candidates, attempt: attempt})
		receipt = mergeProviderTrace(receipt, trace)
		if err == nil {
			receipt.Status = providerSuccess
			receipt.ElapsedMillis = time.Since(started).Milliseconds()
			if p.Settings.TraceMode != "" && !strings.EqualFold(p.Settings.TraceMode, "off") {
				receipt.capToolResults(p.Settings.TraceFullResultBytes)
				proposal.TraceReceipt = &receipt
			}
			proposal.Audit = audit
			return proposal, nil
		}
		if !isCorrectableProviderError(err) || attempt == p.Settings.MaxRetries {
			return nil, err
		}
		correction = "\n\n=== CORRECTION REQUIRED ===\nThe previous response was structurally invalid. Submit only the permitted terminal tool with valid repository-relative paths."
	}
	return nil, errors.New("provider corrective attempts exhausted")
}

func (p ProviderProposer) refactorPrompt(summary string, candidates CandidatePacket, source string, audit *AuditContext) string {
	prompt := strings.TrimSpace(p.Settings.RefactorPrompt)
	if (p.Settings.RefactorMode == "" || strings.EqualFold(p.Settings.RefactorMode, "oneshot")) && len(candidates.Files) > 0 && strings.TrimSpace(p.Settings.OneShotRefactorPrompt) != "" {
		prompt = p.Settings.OneShotRefactorPrompt
	}
	if prompt == "" {
		prompt = "Find one small machine-verifiable deletion or consolidation. Submit exactly one terminal result: submit_proposal or submit_deletions when supported, otherwise submit_no_candidate."
	}
	if p.Settings.useSymbolDeletions() {
		prompt += " Return supported symbol deletions through submit_deletions."
	} else {
		prompt += " Return supported whole-file replacements through submit_proposal."
	}
	prompt += " A no-candidate result is scoped to checked evidence and stated limitations, never the whole repository."
	if encoded, err := json.Marshal(candidates); err == nil {
		prompt += "\n\n=== CANDIDATE PACKET ===\n" + string(encoded)
	}
	if audit != nil {
		if encoded, err := json.Marshal(audit); err == nil {
			prompt += "\n\n=== REPOSITORY AUDIT CONTEXT ===\n" + string(encoded)
		}
	}
	if strings.TrimSpace(p.Settings.CandidatePacketPrompt) != "" {
		prompt += "\n" + p.Settings.CandidatePacketPrompt
	}
	return prompt + "\n\n=== REQUEST SUMMARY ===\n" + summary + "\n\n=== REPOSITORY SOURCE ===\n" + source
}

func conversationBytes(messages []provider.Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.Content)
		for _, call := range message.ToolCalls {
			total += len(call.Function.Arguments)
		}
	}
	return total
}

func decodeProviderProposal(raw string) (*Proposal, error) {
	var proposal Proposal
	if err := json.Unmarshal([]byte(raw), &proposal); err != nil {
		return nil, fmt.Errorf("decode provider proposal: %w", err)
	}
	if strings.TrimSpace(proposal.Rationale) == "" {
		return nil, errors.New("provider proposal is missing rationale")
	}
	return &proposal, nil
}

func validateProviderProposal(reader *providerReader, proposal *Proposal) error {
	if err := ValidateProposal(reader.root, proposal.Changes); err != nil {
		return fmt.Errorf("validate provider proposal: %w", err)
	}
	if proposal.CandidatePacket == nil {
		return nil
	}
	for _, file := range proposal.CandidatePacket.Files {
		if _, _, err := reader.path(file.Path); err != nil {
			return fmt.Errorf("validate candidate packet path: %w", err)
		}
	}
	return nil
}
