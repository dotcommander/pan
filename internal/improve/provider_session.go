package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/provider"
)

type providerAttemptInput struct {
	ctx            context.Context
	reader         *providerReader
	system, prompt string
	candidates     CandidatePacket
	attempt        int
}

func (p ProviderProposer) proposeRepositoryAttempt(in providerAttemptInput) (*Proposal, ProviderTrace, error) {
	ctx, reader, system, prompt, candidates, attempt := in.ctx, in.reader, in.system, in.prompt, in.candidates, in.attempt
	messages := []provider.Message{{Role: providerSystemRole, Content: system}, {Role: providerUserRole, Content: prompt}}
	iterations := p.Settings.iterationsFor(candidates)
	model := p.Model
	if attempt == p.Settings.MaxRetries && attempt > 0 && p.Settings.EscalationModel != "" {
		model = p.Settings.EscalationModel
	}
	trace := ProviderTrace{Model: model}
	for step := 0; step < iterations; step++ {
		if err := ctx.Err(); err != nil {
			return nil, trace, err
		}
		if conversationBytes(messages) > p.Settings.conversationBytes() {
			return nil, trace, errors.New("provider conversation byte budget exceeded")
		}
		response, err := p.Client.Complete(ctx, provider.Request{Model: model, Messages: messages, Tools: providerTools(p.Settings.useSymbolDeletions())})
		if err != nil {
			return nil, trace, fmt.Errorf("request repository proposal: %w", err)
		}
		trace = responseTrace(trace, response)
		proposal, updated, err := p.consumeRepositoryResponse(providerResponseInput{ctx: ctx, reader: reader, response: response, candidates: candidates, step: step, iterations: iterations, messages: messages, trace: &trace})
		if err != nil {
			return nil, trace, correctableProviderError{err}
		}
		if proposal != nil {
			return proposal, trace, nil
		}
		messages = updated
	}
	return nil, trace, correctableProviderError{fmt.Errorf("provider proposal exceeded %d tool iterations without submission", iterations)}
}

type providerResponseInput struct {
	ctx              context.Context
	reader           *providerReader
	response         provider.Response
	candidates       CandidatePacket
	step, iterations int
	messages         []provider.Message
	trace            *ProviderTrace
}

func (p ProviderProposer) consumeRepositoryResponse(in providerResponseInput) (*Proposal, []provider.Message, error) {
	reader, response, candidates, step, iterations, messages, trace := in.reader, in.response, in.candidates, in.step, in.iterations, in.messages, in.trace
	if len(response.Choices) != 1 {
		return nil, nil, errors.New("provider proposal requires exactly one choice")
	}
	message := response.Choices[0].Message
	if len(message.ToolCalls) == 0 {
		return p.noToolProposal(message.Content, candidates)
	}
	messages = append(messages, message)
	for _, call := range message.ToolCalls {
		if conversationBytes(messages) > p.Settings.conversationBytes() {
			return nil, nil, errors.New("provider conversation byte budget exceeded")
		}
		if isProviderExplorationTool(call.Function.Name) && len(trace.Tools) >= iterations {
			return nil, nil, fmt.Errorf("provider proposal exceeded %d exploration tool calls", iterations)
		}
		proposal, toolMessage, err := p.handleProviderCall(providerCallInput{ctx: in.ctx, reader: reader, call: call, candidates: candidates, step: step, iterations: iterations, trace: trace})
		if err != nil {
			return nil, nil, err
		}
		if proposal != nil {
			trace.AcceptedTerminalCount++
			trace.TerminalTool = call.Function.Name
			return proposal, nil, nil
		}
		messages = append(messages, *toolMessage)
	}
	return nil, messages, nil
}

func (p ProviderProposer) noToolProposal(content string, candidates CandidatePacket) (*Proposal, []provider.Message, error) {
	proposal, err := decodeProviderProposal(content)
	if err != nil || len(proposal.Changes) != 0 || p.Settings.TraceMode == "" {
		return nil, nil, errors.New("provider must submit a terminal proposal tool call")
	}
	proposal.Source, proposal.CandidatePacket = providerSource, &candidates
	return proposal, nil, nil
}

type providerCallInput struct {
	ctx              context.Context
	reader           *providerReader
	call             provider.ToolCall
	candidates       CandidatePacket
	step, iterations int
	trace            *ProviderTrace
}

func (p ProviderProposer) handleProviderCall(in providerCallInput) (*Proposal, *provider.Message, error) {
	ctx, reader, call, candidates, step, iterations, trace := in.ctx, in.reader, in.call, in.candidates, in.step, in.iterations, in.trace
	if call.Function.Name == providerSubmit {
		return p.submitProposal(reader, call, candidates)
	}
	if call.Function.Name == providerDeletions {
		return p.submitDeletions(reader, call, candidates)
	}
	if call.Function.Name == providerNoCandidate {
		return p.submitNoCandidate(call, candidates)
	}
	if !isProviderExplorationTool(call.Function.Name) {
		return nil, nil, fmt.Errorf("provider requested unsupported tool %q", call.Function.Name)
	}
	result, err := reader.call(ctx, call.Function.Name, call.Function.Arguments)
	if err != nil {
		result = "tool refused: " + err.Error()
	}
	trace.recordTool(p.Settings.TraceMode, call.Function.Name, result, err)
	if p.Settings.AgentBudgetThresholdPct > 0 && (step+1)*100 >= iterations*p.Settings.AgentBudgetThresholdPct {
		result += "\n[BUDGET LOW] Submit your final proposal now."
	}
	return nil, &provider.Message{Role: providerToolRole, ToolCallID: call.ID, Content: result}, nil
}

func (p ProviderProposer) submitNoCandidate(call provider.ToolCall, candidates CandidatePacket) (*Proposal, *provider.Message, error) {
	var result struct {
		Rationale   string   `json:"rationale"`
		Checked     []string `json:"checked"`
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &result); err != nil {
		return nil, nil, fmt.Errorf("decode provider no-candidate result: %w", err)
	}
	result.Rationale = strings.TrimSpace(result.Rationale)
	result.Checked = compactEvidence(result.Checked)
	result.Limitations = compactEvidence(append(result.Limitations, candidates.SkippedSignals...))
	if result.Rationale == "" {
		return nil, nil, errors.New("provider no-candidate result is missing rationale")
	}
	if len(result.Checked) == 0 {
		return nil, nil, errors.New("provider no-candidate result is missing checked evidence")
	}
	return &Proposal{Rationale: result.Rationale, Changes: []FileChange{}, Disposition: string(OutcomeNoCandidate), Checked: result.Checked, Limitations: result.Limitations, CandidatePacket: &candidates, Source: providerSource}, nil, nil
}

func compactEvidence(values []string) []string {
	result := make([]string, 0, min(len(values), 50))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
		if len(result) == 50 {
			break
		}
	}
	return result
}

func (p ProviderProposer) submitProposal(reader *providerReader, call provider.ToolCall, candidates CandidatePacket) (*Proposal, *provider.Message, error) {
	if p.Settings.useSymbolDeletions() {
		return nil, nil, errors.New("provider used whole-file terminal in symbol mode")
	}
	proposal, err := decodeProviderProposal(call.Function.Arguments)
	if err != nil {
		return nil, nil, err
	}
	if err = validateProviderProposal(reader, proposal); err != nil {
		return nil, nil, err
	}
	proposal.Source = providerSource
	if proposal.CandidatePacket == nil {
		proposal.CandidatePacket = &candidates
	}
	return proposal, nil, nil
}

func (p ProviderProposer) submitDeletions(reader *providerReader, call provider.ToolCall, candidates CandidatePacket) (*Proposal, *provider.Message, error) {
	if !p.Settings.useSymbolDeletions() {
		return nil, nil, errors.New("provider used symbol terminal in whole-file mode")
	}
	proposal, err := decodeProviderDeletions(reader, call.Function.Arguments)
	if err != nil {
		return nil, nil, err
	}
	proposal.Source, proposal.CandidatePacket = providerSource, &candidates
	return proposal, nil, nil
}
