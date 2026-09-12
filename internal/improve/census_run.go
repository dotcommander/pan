package improve

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CensusOptions configures one guarded coverage census run. Zero
// TestTimeout selects the toolchain default.
type CensusOptions struct {
	RepoPath     string
	CoverProfile string
	TestTimeout  time.Duration
	Exclude      []string
}

// RunCensus executes one guarded coverage census: copy the target work
// tree into an isolated temporary directory, run its suite with coverage
// inside the copy, and fold the measured profile into the deterministic
// census. The target repository is never written; census is read-only
// and never appends a history record.
func RunCensus(ctx context.Context, opts CensusOptions) (CensusReport, error) {
	if opts.CoverProfile != "" {
		blocks, mode, err := ParseCoverProfile(opts.CoverProfile, ReadModulePath(opts.RepoPath))
		if err != nil {
			return CensusReport{}, err
		}
		report := censusFromFiles(FileCoverages(blocks))
		report.Mode = mode
		return report, nil
	}
	if err := LookGo(); err != nil {
		return CensusReport{}, err
	}
	copyDir, err := IsolateWorkTree(opts.RepoPath)
	if err != nil {
		return CensusReport{}, err
	}
	defer removeTree(copyDir)
	result, err := Toolchain{TestTimeout: opts.TestTimeout}.RunTests(ctx, copyDir)
	if err != nil {
		return CensusReport{}, fmt.Errorf("census suite: %w", err)
	}
	return censusFromFiles(result.Files), nil
}

// censusFromFiles folds per-file coverage into the deterministic
// per-package census. Package names are the slash directories of the
// repo-relative file paths, so the rollup needs no module metadata.
func censusFromFiles(files []FileCoverage) CensusReport {
	report := CensusReport{
		Schema:           CensusSchema,
		Mode:             "set",
		CoverageMeasured: true,
		Packages:         []PackageCensus{},
	}
	if len(files) == 0 {
		return report
	}
	byPackage := make(map[string]*PackageCensus)
	var names []string
	for _, file := range files {
		pkg := slashDir(file.File)
		item := byPackage[pkg]
		if item == nil {
			item = &PackageCensus{Package: pkg}
			byPackage[pkg] = item
			names = append(names, pkg)
		}
		item.TotalStatements += file.TotalStatements
		report.TotalStatements += file.TotalStatements
		item.CoveredStatements += file.CoveredStatements
		report.CoveredStatements += file.CoveredStatements
		item.ReachableUncovered += file.UncoveredStatements
	}
	report.Files = len(files)
	sort.Strings(names)
	for _, name := range names {
		item := byPackage[name]
		if item.TotalStatements > 0 {
			item.Coverage = percent(item.CoveredStatements, item.TotalStatements)
			item.TractabilityDensity = round2(float64(item.ReachableUncovered) / float64(item.TotalStatements))
		}
		report.Packages = append(report.Packages, *item)
	}
	report.TotalCoverage = percent(report.CoveredStatements, report.TotalStatements)
	return report
}

// slashDir returns the slash directory of a slash-relative path; a bare
// file name maps to ".".
func slashDir(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return "."
}
