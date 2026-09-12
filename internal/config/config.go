// Package config owns pan's runtime limits. All default values live in the
// embedded default-config.yaml asset; Go source contains no configuration
// data. Zero values in a Config mean "use the embedded default" so partially
// populated user config files stay valid.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed default-config.yaml
var defaults []byte

// Config is pan's effective runtime configuration: every bounded-analysis
// limit plus the improve, language-server, and clean policy rules. Zero
// values mean "use the embedded default" so a partially populated user
// config file stays valid; Normalized fills them in.
type Config struct {
	MaxFiles        int           `yaml:"max_files" json:"max_files"`
	MaxFileBytes    int64         `yaml:"max_file_bytes" json:"max_file_bytes"`
	MaxTotalBytes   int64         `yaml:"max_total_bytes" json:"max_total_bytes"`
	MaxNodes        int           `yaml:"max_nodes" json:"max_nodes"`
	MaxInstructions int           `yaml:"max_instructions" json:"max_instructions"`
	CommandTimeout  time.Duration `yaml:"command_timeout" json:"command_timeout"`
	OutputBudget    int           `yaml:"output_budget" json:"output_budget"`
	Exclude         []string      `yaml:"exclude" json:"exclude"`
	Improve         ImproveRules  `yaml:"improve" json:"improve"`
	Lsp             LspRules      `yaml:"lsp" json:"lsp"`
	Clean           CleanRules    `yaml:"clean" json:"clean"`

	// cleanExplicit records clean keys present in the Pan config source. It is
	// intentionally not serialized: it only resolves compatibility precedence
	// while constructing the effective clean policy.
	cleanExplicit map[string]bool
}

// LspRules owns every language-server policy value: the detection walk
// depth, the per-query timeout, and the language table mapping file types
// and root markers to candidate server commands. The table is
// configuration data; Go source contains logic only. Zero values mean
// "use the embedded default" so a partially populated user file stays
// valid.
type LspRules struct {
	StatusMaxDepth int           `yaml:"status_max_depth" json:"status_max_depth"`
	Timeout        time.Duration `yaml:"timeout" json:"timeout"`
	Languages      []LspLanguage `yaml:"languages" json:"languages"`
}

// LspLanguage is one language-server mapping: which file types and root
// markers identify the language, and which server commands may serve it in
// preference order. Language doubles as the LSP language id.
type LspLanguage struct {
	Language    string   `yaml:"language" json:"language"`
	FileTypes   []string `yaml:"file_types" json:"file_types"`
	RootMarkers []string `yaml:"root_markers" json:"root_markers"`
	Servers     []string `yaml:"servers" json:"servers"`
}

// ImproveRules owns every guarded improvement policy value: the prep
// coverage floor, the target test-suite timeout, the isolation branch
// prefix, the prep worklist bound, the recent-history aggregation window,
// and the optional state directory override (empty resolves to the user
// config directory). All values are configuration data; zero values mean
// "use the embedded default" so a partially populated user file stays
// valid.
type ImproveRules struct {
	CoverageFloor      float64       `yaml:"coverage_floor" json:"coverage_floor"`
	TestTimeout        time.Duration `yaml:"test_timeout" json:"test_timeout"`
	BranchPrefix       string        `yaml:"branch_prefix" json:"branch_prefix"`
	MaxWorklistTargets int           `yaml:"max_worklist_targets" json:"max_worklist_targets"`
	RecentWindow       int           `yaml:"recent_window" json:"recent_window"`
	StateDir           string        `yaml:"state_dir" json:"state_dir"`
}

// CleanExpectedFile is one repository-completeness expectation: any of
// Names satisfies it, Severity weights the score deduction, and Why explains
// the gap in human terms.
type CleanExpectedFile struct {
	Names    []string `yaml:"names" json:"names"`
	Severity string   `yaml:"severity" json:"severity"`
	Why      string   `yaml:"why" json:"why"`
}

// CleanRules owns every cleanup policy value: walk bounds, the archive
// destination, and the pattern lists that drive delete/archive/untrack/
// misplaced-artifact decisions plus the completeness checklist. All values
// are configuration data; Go source contains logic only. Zero values mean
// "use the embedded default" so a partially populated user file stays valid,
// and an explicitly set empty pattern list falls back to the default list.
type CleanRules struct {
	MaxDepth              int                 `yaml:"max_depth" json:"max_depth"`
	StaleDays             int                 `yaml:"stale_days" json:"stale_days"`
	LargeFileBytes        int64               `yaml:"large_file_bytes" json:"large_file_bytes"`
	MaxHistoryCommits     int                 `yaml:"max_history_commits" json:"max_history_commits"`
	ArchiveDir            string              `yaml:"archive_dir" json:"archive_dir"`
	IgnoreFile            string              `yaml:"ignore_file" json:"ignore_file"`
	DeleteNames           []string            `yaml:"delete_names" json:"delete_names"`
	DeleteExtensions      []string            `yaml:"delete_extensions" json:"delete_extensions"`
	DeleteDirectories     []string            `yaml:"delete_directories" json:"delete_directories"`
	ArchiveExtensions     []string            `yaml:"archive_extensions" json:"archive_extensions"`
	DataExtensions        []string            `yaml:"data_extensions" json:"data_extensions"`
	ImageExtensions       []string            `yaml:"image_extensions" json:"image_extensions"`
	UntrackExtensions     []string            `yaml:"untrack_extensions" json:"untrack_extensions"`
	UntrackDirectories    []string            `yaml:"untrack_directories" json:"untrack_directories"`
	AllowedRootMD         []string            `yaml:"allowed_root_md" json:"allowed_root_md"`
	DevArtifactPrefixes   []string            `yaml:"dev_artifact_prefixes" json:"dev_artifact_prefixes"`
	DevArtifactSuffixes   []string            `yaml:"dev_artifact_suffixes" json:"dev_artifact_suffixes"`
	SafeDirectories       []string            `yaml:"safe_directories" json:"safe_directories"`
	IgnoredDevDocSuffixes []string            `yaml:"ignored_dev_doc_suffixes" json:"ignored_dev_doc_suffixes"`
	IgnoredDeletePrefixes []string            `yaml:"ignored_delete_prefixes" json:"ignored_delete_prefixes"`
	DotfileAllowlist      []string            `yaml:"dotfile_allowlist" json:"dotfile_allowlist"`
	ScaffoldFiles         []string            `yaml:"scaffold_files" json:"scaffold_files"`
	CompletenessExpected  []CleanExpectedFile `yaml:"completeness_expected" json:"completeness_expected"`
	CIPatterns            []string            `yaml:"ci_patterns" json:"ci_patterns"`
	BuildPatterns         []string            `yaml:"build_patterns" json:"build_patterns"`
}

// Default returns the configuration encoded in the embedded defaults asset.
func Default() Config {
	var cfg Config
	if err := yaml.Unmarshal(defaults, &cfg); err != nil {
		// The asset is embedded at compile time; a decode failure is a build defect.
		panic(fmt.Sprintf("config: decode embedded defaults: %v", err))
	}
	return cfg
}

// Load returns the effective configuration, seeding the user config file from
// the embedded defaults on first run.
func Load() (Config, error) {
	path, err := userConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if seedErr := writeConfigFile(path); seedErr != nil {
			return Config{}, seedErr
		}
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return Decode(data)
}

// writeConfigFile creates path's parent directory and writes the embedded
// defaults to it. It is the single seeding path shared by Load and InitAt.
func writeConfigFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, defaults, 0o600); err != nil {
		return fmt.Errorf("seed config: %w", err)
	}
	return nil
}

// Decode parses configuration YAML, rejects explicitly invalid values, and
// fills any missing value from the embedded defaults.
func Decode(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	explicit, err := cleanFieldsPresent(data)
	if err != nil {
		return Config{}, fmt.Errorf("decode config fields: %w", err)
	}
	cfg.cleanExplicit = explicit
	// Zero values mean "use the embedded default", so fill defaults
	// before enforcing invariants: a partially populated user config
	// with no clean: section stays valid.
	cfg = cfg.Normalized()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// cleanFieldsPresent returns the clean keys specified in the source YAML.
// Normalization intentionally erases zero values, so this small provenance
// map is required to let an explicit Pan setting outrank legacy compatibility
// settings that happen to equal a Pan default.
func cleanFieldsPresent(data []byte) (map[string]bool, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	fields := make(map[string]bool)
	if len(document.Content) == 0 {
		return fields, nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return fields, nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "clean" || root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		clean := root.Content[i+1]
		for j := 0; j+1 < len(clean.Content); j += 2 {
			fields[clean.Content[j].Value] = true
		}
		return fields, nil
	}
	return fields, nil
}

func (c Config) validate() error {
	if c.MaxFiles < 0 {
		return errors.New("config: max_files must not be negative")
	}
	if c.MaxFileBytes < 0 {
		return errors.New("config: max_file_bytes must not be negative")
	}
	if c.MaxTotalBytes < 0 {
		return errors.New("config: max_total_bytes must not be negative")
	}
	if c.MaxNodes < 0 {
		return errors.New("config: max_nodes must not be negative")
	}
	if c.MaxInstructions < 0 {
		return errors.New("config: max_instructions must not be negative")
	}
	if c.CommandTimeout < 0 {
		return errors.New("config: command_timeout must not be negative")
	}
	if c.OutputBudget < 0 {
		return errors.New("config: output_budget must not be negative")
	}
	if err := c.Improve.validate(); err != nil {
		return err
	}
	if err := c.Lsp.validate(); err != nil {
		return err
	}
	return c.Clean.validate()
}

// validate enforces the language-server policy invariants: bounds must not
// be negative, and every language entry needs a name, at least one dotted
// file type, and at least one candidate server command. Language names are
// unique so file-type resolution is deterministic.
func (r LspRules) validate() error {
	if r.StatusMaxDepth < 0 {
		return errors.New("config: lsp.status_max_depth must not be negative")
	}
	if r.Timeout < 0 {
		return errors.New("config: lsp.timeout must not be negative")
	}
	seen := make(map[string]bool, len(r.Languages))
	for i, lang := range r.Languages {
		if lang.Language == "" {
			return fmt.Errorf("config: lsp.languages[%d].language must not be empty", i)
		}
		if seen[lang.Language] {
			return fmt.Errorf("config: lsp.languages[%d].language %q is duplicated", i, lang.Language)
		}
		seen[lang.Language] = true
		if len(lang.FileTypes) == 0 {
			return fmt.Errorf("config: lsp.languages[%d].file_types must not be empty", i)
		}
		for _, fileType := range lang.FileTypes {
			if !strings.HasPrefix(fileType, ".") || len(fileType) < 2 {
				return fmt.Errorf("config: lsp.languages[%d].file_types entry %q must be a dotted extension", i, fileType)
			}
		}
		if len(lang.Servers) == 0 {
			return fmt.Errorf("config: lsp.languages[%d].servers must not be empty", i)
		}
	}
	return nil
}

// Normalized resolves every zero LspRules value from the embedded
// defaults without aliasing the default language table.
func (r LspRules) Normalized() LspRules {
	d := Default().Lsp
	if r.StatusMaxDepth == 0 {
		r.StatusMaxDepth = d.StatusMaxDepth
	}
	if r.Timeout == 0 {
		r.Timeout = d.Timeout
	}
	if len(r.Languages) == 0 {
		r.Languages = slices.Clone(d.Languages)
	}
	return r
}

// validate enforces the improve policy invariants. The coverage floor is a
// fraction in [0,1], the timeout and bounds must be positive where set, and
// the branch prefix must be a valid, relative git branch name so the
// isolated-copy branch can never resemble a flag or an absolute path.
func (r ImproveRules) validate() error {
	if r.CoverageFloor < 0 || r.CoverageFloor > 1 {
		return errors.New("config: improve.coverage_floor must be between 0 and 1")
	}
	if r.TestTimeout < 0 {
		return errors.New("config: improve.test_timeout must not be negative")
	}
	if r.MaxWorklistTargets < 0 {
		return errors.New("config: improve.max_worklist_targets must not be negative")
	}
	if r.RecentWindow < 0 {
		return errors.New("config: improve.recent_window must not be negative")
	}
	if r.BranchPrefix != "" {
		if strings.HasPrefix(r.BranchPrefix, "-") || strings.HasPrefix(r.BranchPrefix, "/") || strings.Contains(r.BranchPrefix, "..") || strings.ContainsAny(r.BranchPrefix, " ~^:?*[\\\x00") {
			return errors.New("config: improve.branch_prefix must be a relative git branch name")
		}
	}
	if r.StateDir != "" && !filepath.IsAbs(r.StateDir) {
		return errors.New("config: improve.state_dir must be an absolute path or empty")
	}
	return nil
}

// isCleanSeverity reports whether s is one of the accepted completeness
// severity labels.
func isCleanSeverity(s string) bool {
	switch s {
	case "error", "warning", "info":
		return true
	default:
		return false
	}
}

func (r CleanRules) validate() error {
	if r.MaxDepth < 0 {
		return errors.New("config: clean.max_depth must not be negative")
	}
	if r.StaleDays < 0 {
		return errors.New("config: clean.stale_days must not be negative")
	}
	if r.LargeFileBytes < 0 {
		return errors.New("config: clean.large_file_bytes must not be negative")
	}
	if r.MaxHistoryCommits < 0 {
		return errors.New("config: clean.max_history_commits must not be negative")
	}
	if filepath.IsAbs(r.ArchiveDir) {
		return errors.New("config: clean.archive_dir must be relative to the repository root")
	}
	if cleaned := path.Clean(filepath.ToSlash(r.ArchiveDir)); cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("config: clean.archive_dir %q must stay inside the repository root", r.ArchiveDir)
	}
	for i, expected := range r.CompletenessExpected {
		if len(expected.Names) == 0 {
			return fmt.Errorf("config: clean.completeness_expected[%d].names must not be empty", i)
		}
		if !isCleanSeverity(expected.Severity) {
			return fmt.Errorf("config: clean.completeness_expected[%d].severity must be error, warning, or info", i)
		}
	}
	return nil
}

// Normalized resolves zero values from the embedded defaults and
// canonicalizes Exclude without aliasing the input slice.
func (c Config) Normalized() Config {
	d := Default()
	if c.MaxFiles == 0 {
		c.MaxFiles = d.MaxFiles
	}
	if c.MaxFileBytes == 0 {
		c.MaxFileBytes = d.MaxFileBytes
	}
	if c.MaxTotalBytes == 0 {
		c.MaxTotalBytes = d.MaxTotalBytes
	}
	if c.MaxNodes == 0 {
		c.MaxNodes = d.MaxNodes
	}
	if c.MaxInstructions == 0 {
		c.MaxInstructions = d.MaxInstructions
	}
	if c.CommandTimeout == 0 {
		c.CommandTimeout = d.CommandTimeout
	}
	if c.OutputBudget == 0 {
		c.OutputBudget = d.OutputBudget
	}
	if len(c.Exclude) == 0 {
		c.Exclude = slices.Clone(d.Exclude)
	}
	c.Exclude = NormalizeExcludes(c.Exclude)
	c.Improve = c.Improve.Normalized()
	c.Lsp = c.Lsp.Normalized()
	c.Clean = c.Clean.Normalized()
	return c
}

// Normalized resolves every zero ImproveRules value from the embedded
// defaults.
func (r ImproveRules) Normalized() ImproveRules {
	d := Default().Improve
	if r.CoverageFloor == 0 {
		r.CoverageFloor = d.CoverageFloor
	}
	if r.TestTimeout == 0 {
		r.TestTimeout = d.TestTimeout
	}
	if r.BranchPrefix == "" {
		r.BranchPrefix = d.BranchPrefix
	}
	if r.MaxWorklistTargets == 0 {
		r.MaxWorklistTargets = d.MaxWorklistTargets
	}
	if r.RecentWindow == 0 {
		r.RecentWindow = d.RecentWindow
	}
	return r
}

// ImproveStateDir resolves the directory holding improve run history and
// run packets: an explicitly configured state_dir when present, otherwise
// <user config dir>/pan/improve. It never creates or modifies anything.
func (r ImproveRules) ImproveStateDir() (string, error) {
	if r.StateDir != "" {
		return filepath.Abs(r.StateDir)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve improve state directory: %w", err)
	}
	return filepath.Join(base, "pan", "improve"), nil
}

// Normalized resolves every zero CleanRules value from the embedded
// defaults without aliasing the default slices.
func (r CleanRules) Normalized() CleanRules {
	d := Default().Clean
	if r.MaxDepth == 0 {
		r.MaxDepth = d.MaxDepth
	}
	if r.StaleDays == 0 {
		r.StaleDays = d.StaleDays
	}
	if r.LargeFileBytes == 0 {
		r.LargeFileBytes = d.LargeFileBytes
	}
	if r.MaxHistoryCommits == 0 {
		r.MaxHistoryCommits = d.MaxHistoryCommits
	}
	if r.ArchiveDir == "" {
		r.ArchiveDir = d.ArchiveDir
	}
	if r.IgnoreFile == "" {
		r.IgnoreFile = d.IgnoreFile
	}
	stringLists := []*[]string{
		&r.DeleteNames, &r.DeleteExtensions, &r.DeleteDirectories, &r.ArchiveExtensions,
		&r.DataExtensions, &r.ImageExtensions, &r.UntrackExtensions, &r.UntrackDirectories,
		&r.AllowedRootMD, &r.DevArtifactPrefixes, &r.DevArtifactSuffixes, &r.SafeDirectories,
		&r.IgnoredDevDocSuffixes, &r.IgnoredDeletePrefixes, &r.DotfileAllowlist,
		&r.ScaffoldFiles, &r.CIPatterns, &r.BuildPatterns,
	}
	defaults := [][]string{
		d.DeleteNames, d.DeleteExtensions, d.DeleteDirectories, d.ArchiveExtensions,
		d.DataExtensions, d.ImageExtensions, d.UntrackExtensions, d.UntrackDirectories,
		d.AllowedRootMD, d.DevArtifactPrefixes, d.DevArtifactSuffixes, d.SafeDirectories,
		d.IgnoredDevDocSuffixes, d.IgnoredDeletePrefixes, d.DotfileAllowlist,
		d.ScaffoldFiles, d.CIPatterns, d.BuildPatterns,
	}
	for i, list := range stringLists {
		if len(*list) == 0 {
			*list = slices.Clone(defaults[i])
		}
	}
	if len(r.CompletenessExpected) == 0 {
		r.CompletenessExpected = slices.Clone(d.CompletenessExpected)
	}
	return r
}

// NormalizeExcludes cleans, dedupes, and sorts repo-relative exclusion paths.
// Entries are matched against slash-separated paths relative to the repository
// root, so "vendor" excludes "vendor" and everything beneath it.
func NormalizeExcludes(entries []string) []string {
	if len(entries) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		cleaned := strings.TrimPrefix(path.Clean(filepath.ToSlash(strings.TrimSpace(entry))), "/")
		if cleaned == "" || cleaned == "." {
			continue
		}
		if _, dup := seen[cleaned]; dup {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	sort.Strings(out)
	return out
}

func userConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "pan", "config.yaml"), nil
}

// UserPath reports the user configuration file path without creating or
// modifying any file.
func UserPath() (string, error) { return userConfigPath() }

// InitOutcome reports one config init decision: the action taken, the
// absolute target path, and the byte count of the seeded file.
type InitOutcome struct {
	Action string `json:"action"` // created, skipped, or overwritten
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
}

// InitAt seeds the configuration file at path from the embedded defaults.
// An existing file is refused — reported as skipped with no write — unless
// force is set. The written file is read back and validated before the
// outcome is reported, so a created config is always a loadable one.
func InitAt(path string, force bool) (InitOutcome, error) {
	if path == "" {
		return InitOutcome{}, errors.New("config: init path is required")
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	info, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return InitOutcome{}, fmt.Errorf("config: stat %s: %w", path, err)
	}
	action := "created"
	if err == nil {
		if info.IsDir() {
			return InitOutcome{}, fmt.Errorf("config: %s is a directory", path)
		}
		if !force {
			return InitOutcome{Action: "skipped", Path: path}, nil
		}
		action = "overwritten"
	}
	if writeErr := writeConfigFile(path); writeErr != nil {
		return InitOutcome{}, writeErr
	}
	written, err := os.ReadFile(path)
	if err != nil {
		return InitOutcome{}, fmt.Errorf("config: verify %s: %w", path, err)
	}
	if _, err := Decode(written); err != nil {
		return InitOutcome{}, fmt.Errorf("config: verify %s: %w", path, err)
	}
	return InitOutcome{Action: action, Path: path, Bytes: len(written)}, nil
}

// FileCheck reports the validation facts for one configuration file path:
// whether it exists, whether it decodes as valid pan configuration, and the
// first validation error when it does not.
type FileCheck struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Valid  bool   `json:"valid"`
	Error  string `json:"error,omitempty"`
}

// CheckFile validates the configuration file at path without writing
// anything. A missing file is reported as a fact (Exists false), not an
// error, so callers can distinguish "defaults in effect" from an
// unreadable file.
func CheckFile(path string) (FileCheck, error) {
	if path == "" {
		return FileCheck{}, errors.New("config: check path is required")
	}
	check := FileCheck{Path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return check, nil
	}
	if err != nil {
		return check, fmt.Errorf("config: read %s: %w", path, err)
	}
	check.Exists = true
	if _, decErr := Decode(data); decErr != nil {
		check.Error = decErr.Error()
	} else {
		check.Valid = true
	}
	return check, nil
}

// State reports the effective configuration source without creating or
// modifying any file: source is "user" when a readable user config file
// exists and "defaults" otherwise; path is that file's path when known.
func State() (source, path string) {
	configPath, err := userConfigPath()
	if err != nil {
		return "defaults", ""
	}
	if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
		return "user", configPath
	}
	return "defaults", configPath
}
