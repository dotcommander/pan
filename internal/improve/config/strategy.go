package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type promptHashes struct {
	System          string `json:"system"`
	Refactor        string `json:"refactor"`
	CandidatePacket string `json:"candidate_packet"`
	OneShot         string `json:"oneshot"`
	Prep            string `json:"prep"`
	PrepSystem      string `json:"prep_system"`
	PrepFileSystem  string `json:"prep_file_system"`
}
type strategy struct {
	Provider                  string        `json:"provider"`
	Model                     string        `json:"model"`
	BaseURL                   string        `json:"base_url"`
	EscalationModel           string        `json:"escalation_model"`
	RefactorMode              string        `json:"refactor_mode"`
	ProposalFormat            string        `json:"proposal_format"`
	FeePerLine                float64       `json:"fee_per_line"`
	CoverageGate              bool          `json:"coverage_gate"`
	CoverageTolerance         float64       `json:"coverage_tolerance"`
	MutationGate              bool          `json:"mutation_gate"`
	MutationThreshold         float64       `json:"mutation_threshold"`
	MaxRetries                int           `json:"max_retries"`
	TestTimeout               time.Duration `json:"test_timeout"`
	RequestTimeout            time.Duration `json:"request_timeout"`
	MaxCodebaseBytes          int           `json:"max_codebase_bytes"`
	AgentMaxToolIterations    int           `json:"agent_max_tool_iterations"`
	AgentOneShotMaxIterations int           `json:"agent_oneshot_max_iterations"`
	AgentBudgetThresholdPct   int           `json:"agent_budget_threshold_pct"`
	ProviderMaxRetries        int           `json:"provider_max_retries"`
	ProviderMaxBackoff        time.Duration `json:"provider_max_backoff"`
	ProviderRateLimitBackoff  time.Duration `json:"provider_rate_limit_backoff"`
	AgentConversationMaxBytes int           `json:"agent_conversation_max_bytes"`
	PrepPackageContextBytes   int           `json:"prep_package_context_bytes"`
	PrepPromptSourceBytes     int           `json:"prep_prompt_source_bytes"`
	PrepPromptTargetFuncs     int           `json:"prep_prompt_target_funcs"`
	PrepPromptCoverageGaps    int           `json:"prep_prompt_coverage_gaps"`
	PrepCoverageFloor         float64       `json:"prep_coverage_floor"`
	PrepRequestTimeout        time.Duration `json:"prep_request_timeout"`
	PrepRequestInterval       time.Duration `json:"prep_request_interval"`
	PrepCorrectiveTimeout     time.Duration `json:"prep_corrective_timeout"`
	PrepDefaultMaxFiles       int           `json:"prep_default_max_files"`
	PrepBatchFullSuite        bool          `json:"prep_batch_full_suite"`
	PrepTargetRetries         int           `json:"prep_target_retries"`
	PrepTargetRounds          int           `json:"prep_target_rounds"`
	PrepTimeBudget            time.Duration `json:"prep_time_budget"`
	PrepFileModel             string        `json:"prep_file_model"`
	PrepThinkingEnabled       bool          `json:"prep_thinking_enabled"`
	DeadSymbolsFirst          bool          `json:"dead_symbols_first"`
	Staticcheck               bool          `json:"staticcheck"`
	Prompts                   promptHashes  `json:"prompt_hashes"`
}

// StrategyID preserves the source strategy fingerprint and excludes credentials.
func (c Config) StrategyID() string {
	encoded, err := json.Marshal(c.strategyPayload())
	if err != nil {
		return "" // Validate rejects non-finite numeric configuration.
	}
	sum := sha256.Sum256(encoded)
	return "pan-strategy/v1:" + hex.EncodeToString(sum[:])
}

func (c Config) strategyPayload() strategy {
	return strategy{
		Provider: c.Provider, Model: c.Model, BaseURL: c.BaseURL, EscalationModel: c.EscalationModel,
		RefactorMode: c.RefactorMode, ProposalFormat: c.ProposalFormat, FeePerLine: c.FeePerLine,
		CoverageGate: c.CoverageGate, CoverageTolerance: c.CoverageDrop,
		MutationGate: c.MutationGate, MutationThreshold: c.MutationFloor, MaxRetries: c.MaxRetries,
		TestTimeout: c.TestTimeout, RequestTimeout: c.RequestTimeout, MaxCodebaseBytes: c.MaxCodebaseBytes,
		AgentMaxToolIterations: c.AgentMaxToolIterations, AgentOneShotMaxIterations: c.AgentOneShotMaxIterations,
		AgentBudgetThresholdPct: c.AgentBudgetThresholdPct, ProviderMaxRetries: c.ProviderMaxRetries,
		ProviderMaxBackoff: c.ProviderMaxBackoff, ProviderRateLimitBackoff: c.ProviderRateLimitBackoff,
		AgentConversationMaxBytes: c.AgentConversationMaxBytes, PrepPackageContextBytes: c.PrepPackageContextBytes,
		PrepPromptSourceBytes: c.PrepPromptSourceBytes, PrepPromptTargetFuncs: c.PrepPromptTargetFuncs,
		PrepPromptCoverageGaps: c.PrepPromptCoverageGaps, PrepCoverageFloor: c.CoverageFloor,
		PrepRequestTimeout:  c.PrepTimeout(),
		PrepRequestInterval: c.RequestInterval, PrepCorrectiveTimeout: c.CorrectiveTimeout,
		PrepDefaultMaxFiles: c.MaxFiles, PrepBatchFullSuite: c.PrepBatchFullSuite,
		PrepTargetRetries: c.PrepRetries(), PrepTargetRounds: c.PrepTargetRounds, PrepTimeBudget: c.TimeBudget,
		PrepFileModel: c.PrepFileModel, PrepThinkingEnabled: c.PrepThinkingEnabled, DeadSymbolsFirst: c.DeadSymbolsFirst, Staticcheck: c.Staticcheck,
		Prompts: promptHashes{
			System: hashPrompt(c.SystemPrompt), Refactor: hashPrompt(c.RefactorPrompt),
			CandidatePacket: hashPrompt(c.CandidatePacketPrompt), OneShot: hashPrompt(c.OneShotRefactorPrompt),
			Prep: hashPrompt(c.PrepPrompt), PrepSystem: hashPrompt(c.PrepSystemPrompt),
			PrepFileSystem: hashPrompt(c.PrepFileSystemPrompt),
		},
	}
}

func hashPrompt(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
