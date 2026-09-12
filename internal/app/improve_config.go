package app

import (
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/improve"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

func selectedStrategy(cfg improveconfig.Config) string {
	if !cfg.Explicit {
		return ""
	}
	return cfg.StrategyID()
}

func configuredImprovePolicy(rules config.ImproveRules, stateDir string, cfg improveconfig.Config) (config.ImproveRules, string) {
	if cfg.StateDir != "" {
		stateDir = cfg.StateDir
	}
	if cfg.TestTimeout > 0 {
		rules.TestTimeout = cfg.TestTimeout
	}
	if cfg.Explicit && cfg.BranchPrefix != "" {
		rules.BranchPrefix = cfg.BranchPrefix
	}
	if cfg.Explicit {
		rules.CoverageFloor = cfg.CoverageFloor
	}
	return rules, stateDir
}

func providerSettings(cfg improveconfig.Config) improve.ProviderSettings {
	return improve.ProviderSettings{
		PrepSystemPrompt: cfg.PrepSystemPrompt, EscalationModel: cfg.EscalationModel, MaxRetries: cfg.MaxRetries,
		SystemPrompt: cfg.SystemPrompt, PrepFileSystemPrompt: cfg.PrepFileSystemPrompt,
		PrepFileModel: cfg.PrepFileModel, PrepThinkingEnabled: cfg.PrepThinkingEnabled,
		MaxCodebaseBytes: cfg.MaxCodebaseBytes, RefactorMode: cfg.RefactorMode,
		ProposalFormat: cfg.ProposalFormat, RefactorPrompt: cfg.RefactorPrompt,
		CandidatePacketPrompt: cfg.CandidatePacketPrompt, OneShotRefactorPrompt: cfg.OneShotRefactorPrompt,
		PrepPrompt: cfg.PrepPrompt, AgentMaxToolIterations: cfg.AgentMaxToolIterations,
		AgentOneShotMaxIterations: cfg.AgentOneShotMaxIterations, AgentBudgetThresholdPct: cfg.AgentBudgetThresholdPct,
		TraceMode: cfg.AgentTraceMode, AgentTraceMaxResultBytes: cfg.AgentTraceMaxResultBytes,
		TraceFullResultBytes: cfg.AgentTraceFullResultBytes, AgentConversationMaxBytes: cfg.AgentConversationMaxBytes,
		PrepPackageContextBytes: cfg.PrepPackageContextBytes, PrepPromptSourceBytes: cfg.PrepPromptSourceBytes,
		PrepPromptTargetFuncs: cfg.PrepPromptTargetFuncs, PrepPromptCoverageGaps: cfg.PrepPromptCoverageGaps,
		JinnBin: cfg.JinnBin,
	}
}
