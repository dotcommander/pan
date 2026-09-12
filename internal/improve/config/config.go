// Package config owns Pan improvement-compatible improvement settings. It is kept
// separate from Pan's analysis configuration so provider credentials and
// mutation policy cannot leak into unrelated commands.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config controls one improve workflow. APIKeyEnv names an environment
// variable; credentials are deliberately never stored in this structure's
// serialised form.
type Config struct {
	Execution          `yaml:",inline"`
	Provider           string         `yaml:"provider" json:"provider"`
	BaseURL            string         `yaml:"base_url" json:"base_url"`
	APIKeyEnv          string         `yaml:"api_key_env" json:"api_key_env"`
	AuthHeader         string         `yaml:"auth_header" json:"auth_header"`
	Model              string         `yaml:"model" json:"model"`
	BranchPrefix       string         `yaml:"branch_prefix" json:"branch_prefix"`
	StateDir           string         `yaml:"state_dir" json:"state_dir"`
	TestTimeout        time.Duration  `yaml:"test_timeout" json:"test_timeout"`
	CoverageFloor      float64        `yaml:"coverage_floor" json:"coverage_floor"`
	CoverageGate       bool           `yaml:"coverage_gate" json:"coverage_gate"`
	CoverageDrop       float64        `yaml:"coverage_drop" json:"coverage_drop"`
	MutationGate       bool           `yaml:"mutation_gate" json:"mutation_gate"`
	MutationFloor      float64        `yaml:"mutation_floor" json:"mutation_floor"`
	MaxRetries         int            `yaml:"max_retries" json:"max_retries"`
	RequestTimeout     time.Duration  `yaml:"request_timeout" json:"request_timeout"`
	RequestInterval    time.Duration  `yaml:"request_interval" json:"request_interval"`
	CorrectiveTimeout  time.Duration  `yaml:"corrective_timeout" json:"corrective_timeout"`
	TimeBudget         time.Duration  `yaml:"time_budget" json:"time_budget"`
	ToFloor            bool           `yaml:"to_floor" json:"to_floor"`
	MaxFiles           int            `yaml:"max_files" json:"max_files"`
	FeePerLine         float64        `yaml:"fee_per_line" json:"fee_per_line"`
	PrepRequestTimeout *time.Duration `yaml:"prep_request_timeout" json:"prep_request_timeout,omitempty"`
	PrepTargetRetries  *int           `yaml:"prep_target_retries" json:"prep_target_retries,omitempty"`
	Explicit           bool           `yaml:"-" json:"-"`
}

// Default returns conservative Pan improvement-compatible settings. A caller must
// explicitly select live mode; this configuration never implies it.
func Default() Config {
	return Config{BranchPrefix: "pan-improve", CoverageDrop: 0.5, CoverageFloor: 0.70}
}

// Load reads an improve-only YAML file. It contains configuration, never a
// credential: APIKeyEnv names the environment variable that supplies a key.
func Load(path string) (Config, error) {
	if path == "" {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read improve config: %w", err)
	}
	cfg, err := Decode(data)
	cfg.Explicit = true
	return cfg, err
}

// Decode accepts the isolated Pan improvement-compatible YAML representation.
func Decode(data []byte) (Config, error) {
	if err := validateKeys(data); err != nil {
		return Config{}, err
	}
	cfg := Default()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode improve config: %w", err)
	}
	var legacy sourceCompat
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return Config{}, fmt.Errorf("decode improve compatibility settings: %w", err)
	}
	legacy.apply(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// sourceCompat maps Pan improvement's established prep and gate names to Pan's
// effective gate and prep controls.
type sourceCompat struct {
	CoverageTolerance     *float64       `yaml:"coverage_tolerance"`
	MutationThreshold     *float64       `yaml:"mutation_threshold"`
	PrepCoverageFloor     *float64       `yaml:"prep_coverage_floor"`
	PrepRequestInterval   *time.Duration `yaml:"prep_request_interval"`
	PrepCorrectiveTimeout *time.Duration `yaml:"prep_corrective_timeout"`
	PrepDefaultMaxFiles   *int           `yaml:"prep_default_max_files"`
	PrepTimeBudget        *time.Duration `yaml:"prep_time_budget"`
}

func (legacy sourceCompat) apply(cfg *Config) {
	if legacy.CoverageTolerance != nil {
		cfg.CoverageDrop = *legacy.CoverageTolerance
	}
	if legacy.MutationThreshold != nil {
		cfg.MutationFloor = *legacy.MutationThreshold
	}
	if legacy.PrepCoverageFloor != nil {
		cfg.CoverageFloor = *legacy.PrepCoverageFloor
	}
	if legacy.PrepRequestInterval != nil {
		cfg.RequestInterval = *legacy.PrepRequestInterval
	}
	if legacy.PrepCorrectiveTimeout != nil {
		cfg.CorrectiveTimeout = *legacy.PrepCorrectiveTimeout
	}
	if legacy.PrepDefaultMaxFiles != nil {
		cfg.MaxFiles = *legacy.PrepDefaultMaxFiles
	}
	if legacy.PrepTimeBudget != nil {
		cfg.TimeBudget = *legacy.PrepTimeBudget
	}
}

// Validate rejects unsafe branch names and invalid gate bounds.
func (c Config) Validate() error {
	if err := c.validateNumericBounds(); err != nil {
		return err
	}
	if err := c.validatePaths(); err != nil {
		return err
	}
	return c.validateExecution()
}

func (c Config) validateNumericBounds() error {
	for _, value := range []float64{c.CoverageFloor, c.MutationFloor, c.CoverageDrop, c.FeePerLine} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("improve config: numeric values must be finite")
		}
	}
	if err := c.validateGateBounds(); err != nil {
		return err
	}
	if c.MaxRetries < 0 || c.MaxFiles < 0 || c.TestTimeout < 0 || c.RequestTimeout < 0 || c.RequestInterval < 0 || c.CorrectiveTimeout < 0 || c.TimeBudget < 0 {
		return errors.New("improve config: retry and timeout values must not be negative")
	}
	return nil
}

func (c Config) validateGateBounds() error {
	if c.CoverageFloor < 0 || c.CoverageFloor > 1 || c.MutationFloor < 0 || c.MutationFloor > 1 || c.CoverageDrop < 0 {
		return errors.New("improve config: coverage and mutation bounds are invalid")
	}
	return nil
}

func (c Config) validatePaths() error {
	if c.BranchPrefix == "" || strings.HasPrefix(c.BranchPrefix, "-") || filepath.IsAbs(c.BranchPrefix) || strings.Contains(c.BranchPrefix, "..") || strings.ContainsAny(c.BranchPrefix, " ~^:?*[\\\x00") {
		return errors.New("improve config: branch_prefix must be a safe relative git branch name")
	}
	if c.StateDir != "" && !filepath.IsAbs(c.StateDir) {
		return errors.New("improve config: state_dir must be absolute when set")
	}
	return nil
}
