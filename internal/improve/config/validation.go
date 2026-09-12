package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// validateKeys admits the typed controls and their source aliases without
// silently accepting a misspelled or unsupported setting.
func validateKeys(data []byte) error {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode improve config: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("improve config requires one YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("improve config must be a mapping")
	}
	allowed := map[string]bool{}
	collectKeys(reflect.TypeFor[Config](), allowed)
	collectKeys(reflect.TypeFor[sourceCompat](), allowed)
	for i := 0; i < len(document.Content[0].Content); i += 2 {
		key := document.Content[0].Content[i].Value
		if !allowed[key] {
			return fmt.Errorf("unknown improve config field %q", key)
		}
	}
	return nil
}

func collectKeys(typ reflect.Type, keys map[string]bool) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			collectKeys(field.Type, keys)
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
}

func (c Config) validateExecution() error {
	switch c.RefactorMode {
	case "", "agentic", "oneshot":
	default:
		return errors.New("refactor_mode must be agentic or oneshot")
	}
	switch c.ProposalFormat {
	case "", "files", "symbols", "whole-file":
	default:
		return errors.New("proposal_format must be files or symbols")
	}
	switch c.AgentTraceMode {
	case "", "off", "summary", "full":
	default:
		return errors.New("agent_trace_mode must be off, summary, or full")
	}
	for _, n := range []int{c.MaxCodebaseBytes, c.AgentMaxToolIterations, c.AgentOneShotMaxIterations, c.ProviderMaxRetries, c.PrepPackageContextBytes, c.PrepPromptSourceBytes, c.PrepPromptTargetFuncs, c.PrepPromptCoverageGaps, c.AgentTraceMaxResultBytes, c.AgentTraceFullResultBytes, c.AgentConversationMaxBytes, c.PrepMaxConcurrent, c.PrepTargetRounds, c.PrepRetries()} {
		if n < 0 {
			return errors.New("improve config: execution bounds must not be negative")
		}
	}
	if c.AgentBudgetThresholdPct < 0 || c.AgentBudgetThresholdPct > 100 {
		return errors.New("agent_budget_threshold_pct must be in [0,100]")
	}
	if c.ProviderMaxBackoff < 0 || c.ProviderRateLimitBackoff < 0 || c.PrepTimeout() < 0 {
		return errors.New("improve config: provider and prep durations must not be negative")
	}
	return nil
}

// PrepTimeout preserves the independent source refactor and prep timeouts.
func (c Config) PrepTimeout() time.Duration {
	if c.PrepRequestTimeout != nil {
		return *c.PrepRequestTimeout
	}
	return c.RequestTimeout
}

// PrepRetries preserves an explicitly configured zero corrective retry count.
func (c Config) PrepRetries() int {
	if c.PrepTargetRetries != nil {
		return *c.PrepTargetRetries
	}
	return c.MaxRetries
}
