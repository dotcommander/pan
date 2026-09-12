package app

import (
	"fmt"
	"os"
	"time"

	"github.com/dotcommander/pan/internal/improve"
	improveconfig "github.com/dotcommander/pan/internal/improve/config"
	"github.com/dotcommander/pan/internal/provider"
)

// ImproveProviderOptions selects a named provider or a compatible endpoint.
// Deterministic bypasses provider construction while preserving history identity.
type ImproveProviderOptions struct {
	BaseURL       string
	APIKeyEnv     string
	AuthHeader    string
	Model         string
	Timeout       time.Duration
	StrategyID    string
	Provider      string
	FeePerLine    float64
	Config        improveconfig.Config
	Deterministic bool
}

func (o ImproveProviderOptions) proposalProposer(root string, exclude []string) (improve.Proposer, error) {
	if o.Deterministic || (o.BaseURL == "" && o.Provider == "") {
		return nil, nil
	}
	transport, err := o.transportConfig(o.Timeout)
	if err != nil {
		return nil, err
	}
	client, err := provider.New(transport)
	if err != nil {
		return nil, err
	}
	return improve.ProviderProposer{Client: client, Model: o.Model, Settings: o.providerSettings(root, exclude)}, nil
}

func (o ImproveProviderOptions) testGenerator(root string, exclude []string) (improve.TestGenerator, error) {
	if o.Deterministic || (o.BaseURL == "" && o.Provider == "") {
		return nil, nil
	}
	transport, err := o.transportConfig(o.Config.PrepTimeout())
	if err != nil {
		return nil, err
	}
	client, err := provider.New(transport)
	if err != nil {
		return nil, err
	}
	return improve.ProviderTestGenerator{Client: client, Model: o.Model, Settings: o.providerSettings(root, exclude)}, nil
}

func (o ImproveProviderOptions) transportConfig(timeout time.Duration) (provider.Config, error) {
	apiKey := ""
	if o.APIKeyEnv != "" {
		apiKey = os.Getenv(o.APIKeyEnv)
		if apiKey == "" {
			return provider.Config{}, fmt.Errorf("provider API key environment variable %q is empty", o.APIKeyEnv)
		}
	}
	return provider.Config{Provider: o.Provider, BaseURL: o.BaseURL, APIKey: apiKey, AuthHeader: o.AuthHeader, Model: o.Model, Timeout: timeout,
		ProviderMaxRetries: o.Config.ProviderMaxRetries, MaxProviderBackoff: o.Config.ProviderMaxBackoff, RateLimitBackoff: o.Config.ProviderRateLimitBackoff}, nil
}

func (o ImproveProviderOptions) providerSettings(root string, exclude []string) improve.ProviderSettings {
	settings := providerSettings(o.Config)
	settings.RepoPath, settings.Exclude = root, exclude
	return settings
}
