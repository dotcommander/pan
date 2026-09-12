package app

import (
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/improve"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

func TestImproveConfigOwnsWorkflowAndProviderSettings(t *testing.T) {
	t.Parallel()
	cfg, err := improveconfig.Decode([]byte("state_dir: /fixture/state\nbranch_prefix: source-attempt\ntest_timeout: 7s\nrequest_timeout: 9s\nprep_request_timeout: 2s\nmax_retries: 3\nprep_target_retries: 0\nprep_target_rounds: 2\nprep_batch_full_suite: true\nrefactor_prompt: inspect duplication\nmax_codebase_bytes: 2048\njinn_bin: jinn-fixture\nstaticcheck: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Explicit = true
	rules, state := configuredImprovePolicy(config.ImproveRules{}, "old", cfg)
	assertImproveWorkflowSettings(t, rules, state)
	var prep ImprovePrepOptions
	prep.ApplyConfig(cfg)
	assertImprovePrepSettings(t, prep)
	settings := providerSettings(cfg)
	assertImproveProviderSettings(t, settings, cfg)
	assertImproveStrategyIdentity(t, cfg)
	var implicit ImprovePrepOptions
	implicit.ApplyConfig(improveconfig.Default())
	assertImprovePrepStrategy(t, implicit, prep, cfg)
}

func assertImproveWorkflowSettings(t *testing.T, rules config.ImproveRules, state string) {
	t.Helper()
	if state != "/fixture/state" || rules.BranchPrefix != "source-attempt" || rules.TestTimeout != 7*time.Second {
		t.Fatalf("workflow settings not applied: %#v %s", rules, state)
	}
}

func assertImprovePrepSettings(t *testing.T, prep ImprovePrepOptions) {
	t.Helper()
	if prep.RequestTimeout != 2*time.Second || prep.Retries != 0 || prep.TargetRounds != 2 || !prep.BatchFullSuite {
		t.Fatalf("prep settings not applied: %#v", prep)
	}
}

func assertImproveProviderSettings(t *testing.T, settings improve.ProviderSettings, cfg improveconfig.Config) {
	t.Helper()
	if settings.RefactorPrompt != cfg.RefactorPrompt || settings.MaxCodebaseBytes != 2048 || settings.JinnBin != "jinn-fixture" || !cfg.Staticcheck {
		t.Fatalf("provider settings not applied: %#v", settings)
	}
}

func assertImproveStrategyIdentity(t *testing.T, cfg improveconfig.Config) {
	t.Helper()
	if selectedStrategy(improveconfig.Default()) != "" || selectedStrategy(cfg) != cfg.StrategyID() {
		t.Fatal("implicit configuration must retain unscoped legacy history")
	}
}

func assertImprovePrepStrategy(t *testing.T, implicit, prep ImprovePrepOptions, cfg improveconfig.Config) {
	t.Helper()
	if implicit.StrategyID != selectedStrategy(improveconfig.Default()) || prep.StrategyID != selectedStrategy(cfg) {
		t.Fatal("workflow history and reader strategy filters differ")
	}
}

func TestImproveForcedDeadcodePreservesConfiguredIdentity(t *testing.T) {
	t.Parallel()
	cfg := improveconfig.Default()
	cfg.Explicit, cfg.Provider, cfg.Model = true, "anthropic", "fixture-model"
	opts := ImproveProviderOptions{Provider: cfg.Provider, Model: cfg.Model, APIKeyEnv: "PAN_TEST_MISSING_PROVIDER_KEY", Config: cfg, StrategyID: selectedStrategy(cfg), Deterministic: true}
	proposer, err := opts.proposalProposer(t.TempDir(), nil)
	if err != nil || proposer != nil {
		t.Fatalf("forced deterministic lane constructed a provider: %v", err)
	}
	generator, err := opts.testGenerator(t.TempDir(), nil)
	if err != nil || generator != nil {
		t.Fatalf("forced deterministic lane constructed a test generator: %v", err)
	}
	if opts.StrategyID != cfg.StrategyID() || opts.Provider != cfg.Provider || opts.Model != cfg.Model || opts.Config.DeadSymbolsFirst {
		t.Fatal("execution mode changed configured history identity")
	}
}

func TestImproveProviderCapabilitiesConstructIndependently(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	opts := ImproveProviderOptions{
		BaseURL: "http://127.0.0.1:1",
		Model:   "fixture-model",
		Timeout: 3 * time.Second,
		Config:  improveconfig.Default(),
	}
	proposer, err := opts.proposalProposer(root, []string{"vendor/**"})
	if err != nil {
		t.Fatal(err)
	}
	providerProposer, ok := proposer.(improve.ProviderProposer)
	if !ok || providerProposer.Client == nil || providerProposer.Settings.RepoPath != root || len(providerProposer.Settings.Exclude) != 1 {
		t.Fatalf("proposal capability = %#v", proposer)
	}
	generator, err := opts.testGenerator(root, []string{"vendor/**"})
	if err != nil {
		t.Fatal(err)
	}
	providerGenerator, ok := generator.(improve.ProviderTestGenerator)
	if !ok || providerGenerator.Client == nil || providerGenerator.Settings.RepoPath != root || len(providerGenerator.Settings.Exclude) != 1 {
		t.Fatalf("test-generation capability = %#v", generator)
	}
}

func TestImproveExplicitCoverageFloor(t *testing.T) {
	t.Parallel()
	for _, floor := range []float64{0, 0.9} {
		cfg := improveconfig.Default()
		cfg.Explicit, cfg.CoverageFloor = true, floor
		rules, _ := configuredImprovePolicy(config.ImproveRules{CoverageFloor: 0.8}, "", cfg)
		if rules.CoverageFloor != floor {
			t.Fatalf("floor = %v, want %v", rules.CoverageFloor, floor)
		}
	}
	rules, _ := configuredImprovePolicy(config.ImproveRules{CoverageFloor: 0.8}, "", improveconfig.Default())
	if rules.CoverageFloor != 0.8 {
		t.Fatalf("implicit configuration changed floor to %v", rules.CoverageFloor)
	}
}

func TestImproveOmittedCoverageFloorUsesSourceDefault(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input string
		want  float64
	}{{"branch_prefix: fixture\n", 0.7}, {"coverage_floor: 0\n", 0}, {"prep_coverage_floor: 0\n", 0}} {
		cfg, err := improveconfig.Decode([]byte(tc.input))
		if err != nil {
			t.Fatal(err)
		}
		cfg.Explicit = true
		rules, _ := configuredImprovePolicy(config.ImproveRules{CoverageFloor: 0.8}, "", cfg)
		if rules.CoverageFloor != tc.want {
			t.Fatalf("%q: floor = %v, want %v", tc.input, rules.CoverageFloor, tc.want)
		}
	}
}
