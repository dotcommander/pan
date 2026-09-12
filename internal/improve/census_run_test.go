package improve

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCensusReadsExistingCoverProfileWithoutRunningTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	profile := filepath.Join(dir, "coverage.out")
	writeFile(t, dir, "coverage.out", "mode: set\nsample/demo.go:1.1,1.10 1 1\n")
	report, err := RunCensus(context.Background(), CensusOptions{RepoPath: dir, CoverProfile: profile})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "set" || report.Files != 1 || report.TotalCoverage != 100 {
		t.Fatalf("coverprofile report = %+v", report)
	}
}
