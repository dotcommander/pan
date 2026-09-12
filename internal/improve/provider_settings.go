package improve

import "strings"

// ProviderSettings bounds the source and conversation supplied to an injected
// completion client. Field names mirror Pan improvement's execution settings so app
// wiring remains a direct mapping rather than a second configuration dialect.
type ProviderSettings struct {
	RepoPath string
	Exclude  []string
	// JinnBin optionally selects Pan improvement's compatible local exploration
	// executable. Empty keeps exploration in Pan's native bounded reader.
	JinnBin string

	SystemPrompt              string
	PrepSystemPrompt          string
	PrepFileSystemPrompt      string
	PrepFileModel             string
	PrepThinkingEnabled       bool
	MaxCodebaseBytes          int
	RefactorMode              string
	ProposalFormat            string
	RefactorPrompt            string
	CandidatePacketPrompt     string
	OneShotRefactorPrompt     string
	PrepPrompt                string
	AgentMaxToolIterations    int
	AgentOneShotMaxIterations int
	AgentBudgetThresholdPct   int
	EscalationModel           string
	MaxRetries                int
	TraceMode                 string
	AgentTraceMaxResultBytes  int
	TraceFullResultBytes      int
	AgentConversationMaxBytes int
	PrepPackageContextBytes   int
	PrepPromptSourceBytes     int
	PrepPromptTargetFuncs     int
	PrepPromptCoverageGaps    int
}

func (s ProviderSettings) toolIterations() int {
	if s.AgentMaxToolIterations > 0 {
		return s.AgentMaxToolIterations
	}
	return 30
}

func (s ProviderSettings) iterationsFor(candidates CandidatePacket) int {
	if (s.RefactorMode == "" || strings.EqualFold(s.RefactorMode, "oneshot")) && len(candidates.Files) > 0 && s.AgentOneShotMaxIterations > 0 {
		return s.AgentOneShotMaxIterations
	}
	return s.toolIterations()
}

func (s ProviderSettings) sourceBytes() int {
	if s.MaxCodebaseBytes > 0 {
		return s.MaxCodebaseBytes
	}
	return 64 << 10
}

func (s ProviderSettings) resultBytes() int {
	if s.AgentTraceMaxResultBytes > 0 {
		return s.AgentTraceMaxResultBytes
	}
	return 12 << 10
}

func (s ProviderSettings) conversationBytes() int {
	if s.AgentConversationMaxBytes > 0 {
		return s.AgentConversationMaxBytes
	}
	return 128 << 10
}

func (s ProviderSettings) prepSourceBytes() int {
	if s.PrepPromptSourceBytes > 0 {
		return s.PrepPromptSourceBytes
	}
	return 12 << 10
}

func (s ProviderSettings) prepContextBytes() int {
	if s.PrepPackageContextBytes > 0 {
		return s.PrepPackageContextBytes
	}
	return 8 << 10
}

func (s ProviderSettings) useSymbolDeletions() bool {
	return s.ProposalFormat == "" || strings.EqualFold(s.ProposalFormat, "symbols")
}
