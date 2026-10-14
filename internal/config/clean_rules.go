package config

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// CleanRules owns every cleanup policy value: walk bounds, the archive
// destination, protected names, the pattern lists driving artifact
// dispositions, and the completeness checklist. All values
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
	ProtectedNames        []string            `yaml:"protected_names" json:"protected_names"`
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

func (r CleanRules) validate() error {
	for i, name := range r.ProtectedNames {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			return fmt.Errorf("config: clean.protected_names[%d] must be a file basename", i)
		}
	}
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
		&r.ProtectedNames, &r.AllowedRootMD, &r.DevArtifactPrefixes, &r.DevArtifactSuffixes, &r.SafeDirectories,
		&r.IgnoredDevDocSuffixes, &r.IgnoredDeletePrefixes, &r.DotfileAllowlist,
		&r.ScaffoldFiles, &r.CIPatterns, &r.BuildPatterns,
	}
	defaults := [][]string{
		d.DeleteNames, d.DeleteExtensions, d.DeleteDirectories, d.ArchiveExtensions,
		d.DataExtensions, d.ImageExtensions, d.UntrackExtensions, d.UntrackDirectories,
		d.ProtectedNames, d.AllowedRootMD, d.DevArtifactPrefixes, d.DevArtifactSuffixes, d.SafeDirectories,
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
