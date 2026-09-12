package cli

import (
	"testing"
	"time"

	"github.com/alecthomas/kong"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

func TestImproveExplicitZeroOverridesConfig(t *testing.T) {
	t.Parallel()
	root := &Root{}
	parser, err := kong.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := parser.Parse([]string{"improve", "prep", "--retries=0", "--max-files=0", "--request-interval=0s", "--to-floor=false"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := improveconfig.Default()
	cfg.MaxRetries, cfg.MaxFiles, cfg.RequestInterval, cfg.ToFloor = 3, 5, time.Second, true
	got := root.Improve.Prep.prepConfig(ctx, cfg)
	if got.PrepRetries() != 0 || got.MaxFiles != 0 || got.RequestInterval != 0 || got.ToFloor {
		t.Fatalf("explicit zero options lost: %#v", got)
	}
}

func TestImproveProviderFlagsFingerprintEffectiveModel(t *testing.T) {
	t.Parallel()
	cfg := improveconfig.Default()
	cfg.Model = "old-model"
	cfg.Explicit = true
	got := (providerFlags{ProviderModel: "new-model"}).providerOptions(nil, cfg)
	if got.Config.Model != "new-model" || got.StrategyID != got.Config.StrategyID() || got.StrategyID == cfg.StrategyID() {
		t.Fatalf("provider settings and identity disagree: %#v", got)
	}
}

func TestImproveRefactorOmittedFlagsPreserveConfig(t *testing.T) {
	t.Parallel()
	root := &Root{}
	parser, err := kong.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := parser.Parse([]string{"improve", "refactor", "--coverage-drop=0", "--coverage-gate=false"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := improveconfig.Default()
	cfg.CoverageGate, cfg.AgentTraceMode = true, "summary"
	got := root.Improve.Refactor.refactorConfig(ctx, cfg)
	if got.CoverageDrop != 0 || got.CoverageGate || got.AgentTraceMode != "summary" {
		t.Fatalf("effective gate/trace config = %#v", got)
	}
}

func TestImproveProviderExplicitZeroAndImplicitStrategy(t *testing.T) {
	t.Parallel()
	root := &Root{}
	parser, err := kong.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := parser.Parse([]string{"improve", "probe", "--provider-timeout=0s"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := improveconfig.Default()
	cfg.RequestTimeout = time.Minute
	got := root.Improve.Probe.providerOptions(ctx, cfg)
	if got.Timeout != 0 || got.Config.RequestTimeout != 0 || got.StrategyID != "" {
		t.Fatalf("explicit zero or unscoped history lost: %#v", got)
	}
}
