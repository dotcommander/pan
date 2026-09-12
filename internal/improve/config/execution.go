package config

import "time"

// Execution retains the source provider, prompt, and prep workflow controls.
// Credentials remain caller-owned; these fields never authorize a provider call.
type Execution struct {
	EscalationModel           string        `yaml:"escalation_model" json:"escalation_model"`
	MaxCodebaseBytes          int           `yaml:"max_codebase_bytes" json:"max_codebase_bytes"`
	RefactorMode              string        `yaml:"refactor_mode" json:"refactor_mode"`
	ProposalFormat            string        `yaml:"proposal_format" json:"proposal_format"`
	RefactorPrompt            string        `yaml:"refactor_prompt" json:"refactor_prompt"`
	CandidatePacketPrompt     string        `yaml:"candidate_packet_prompt" json:"candidate_packet_prompt"`
	OneShotRefactorPrompt     string        `yaml:"oneshot_refactor_prompt" json:"oneshot_refactor_prompt"`
	PrepPrompt                string        `yaml:"prep_prompt" json:"prep_prompt"`
	AgentMaxToolIterations    int           `yaml:"agent_max_tool_iterations" json:"agent_max_tool_iterations"`
	AgentOneShotMaxIterations int           `yaml:"agent_oneshot_max_iterations" json:"agent_oneshot_max_iterations"`
	AgentBudgetThresholdPct   int           `yaml:"agent_budget_threshold_pct" json:"agent_budget_threshold_pct"`
	ProviderMaxRetries        int           `yaml:"provider_max_retries" json:"provider_max_retries"`
	ProviderMaxBackoff        time.Duration `yaml:"provider_max_backoff" json:"provider_max_backoff"`
	ProviderRateLimitBackoff  time.Duration `yaml:"provider_rate_limit_backoff" json:"provider_rate_limit_backoff"`
	PrepPackageContextBytes   int           `yaml:"prep_package_context_bytes" json:"prep_package_context_bytes"`
	PrepPromptSourceBytes     int           `yaml:"prep_prompt_source_bytes" json:"prep_prompt_source_bytes"`
	PrepPromptTargetFuncs     int           `yaml:"prep_prompt_target_funcs" json:"prep_prompt_target_funcs"`
	PrepPromptCoverageGaps    int           `yaml:"prep_prompt_coverage_gaps" json:"prep_prompt_coverage_gaps"`
	AgentTraceMode            string        `yaml:"agent_trace_mode" json:"agent_trace_mode"`
	AgentTraceMaxResultBytes  int           `yaml:"agent_trace_max_result_bytes" json:"agent_trace_max_result_bytes"`
	AgentTraceFullResultBytes int           `yaml:"agent_trace_full_result_bytes" json:"agent_trace_full_result_bytes"`
	AgentConversationMaxBytes int           `yaml:"agent_conversation_max_bytes" json:"agent_conversation_max_bytes"`
	SystemPrompt              string        `yaml:"system_prompt" json:"system_prompt"`
	PrepMaxConcurrent         int           `yaml:"prep_max_concurrent" json:"prep_max_concurrent"`
	PrepBatchFullSuite        bool          `yaml:"prep_batch_full_suite" json:"prep_batch_full_suite"`
	PrepTargetRounds          int           `yaml:"prep_target_rounds" json:"prep_target_rounds"`
	PrepSystemPrompt          string        `yaml:"prep_system_prompt" json:"prep_system_prompt"`
	PrepFileSystemPrompt      string        `yaml:"prep_file_system_prompt" json:"prep_file_system_prompt"`
	PrepFileModel             string        `yaml:"prep_file_model" json:"prep_file_model"`
	PrepThinkingEnabled       bool          `yaml:"prep_thinking_enabled" json:"prep_thinking_enabled"`
	JinnBin                   string        `yaml:"jinn_bin" json:"jinn_bin"`
	DeadSymbolsFirst          bool          `yaml:"deadsymbols_first" json:"deadsymbols_first"`
	Staticcheck               bool          `yaml:"staticcheck" json:"staticcheck"`
}
