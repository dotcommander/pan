package config

import "testing"

func TestValidateRejectsUnsafeBranch(t *testing.T) {
	t.Parallel()
	c := Default()
	c.BranchPrefix = "../escape"
	if err := c.Validate(); err == nil {
		t.Fatal("unsafe branch accepted")
	}
}

func TestDecodeLoadsDurationsAndStableStrategyExcludesCredentials(t *testing.T) {
	t.Parallel()
	first, err := Decode([]byte("model: test-model\nrequest_timeout: 2s\napi_key_env: FIRST_KEY\nauth_header: secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Decode([]byte("model: test-model\nrequest_timeout: 2s\napi_key_env: SECOND_KEY\nauth_header: another-secret\nstate_dir: /private/tmp/state\n"))
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestTimeout.String() != "2s" || first.StrategyID() != second.StrategyID() {
		t.Fatalf("config identity = %#v / %#v", first, second)
	}
}

func TestDecodeAcceptsJanitorPrepAliasesAndPrompts(t *testing.T) {
	t.Parallel()
	cfg, err := Decode([]byte("prep_coverage_floor: 0.8\nprep_request_timeout: 3s\nprep_target_retries: 2\nprep_default_max_files: 4\ncoverage_tolerance: 1.5\nrefactor_prompt: remove duplication\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CoverageFloor != 0.8 || cfg.PrepTimeout().String() != "3s" || cfg.PrepRetries() != 2 || cfg.MaxFiles != 4 || cfg.CoverageDrop != 1.5 || cfg.RefactorPrompt != "remove duplication" {
		t.Fatalf("compat config = %#v", cfg)
	}
}

func TestPrepControlsPreserveIndependentRefactorSettings(t *testing.T) {
	t.Parallel()
	cfg, err := Decode([]byte("request_timeout: 9s\nmax_retries: 4\nprep_request_timeout: 2s\nprep_target_retries: 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequestTimeout.String() != "9s" || cfg.MaxRetries != 4 || cfg.PrepTimeout().String() != "2s" || cfg.PrepRetries() != 0 {
		t.Fatalf("independent settings lost: %#v", cfg)
	}
	original := cfg.StrategyID()
	cfg.RefactorPrompt = "different strategy"
	if cfg.StrategyID() == original {
		t.Fatal("prompt change did not change strategy identity")
	}
}

func TestDecodeRejectsUnknownOrInvalidExecutionSettings(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"refactor_promt: typo", "agent_trace_mode: secrets", "prep_target_retries: -1", "provider_max_backoff: -1s", "agent_budget_threshold_pct: 101", "model: first\n---\nmodel: second"} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Errorf("accepted invalid configuration %q", input)
		}
	}
}
