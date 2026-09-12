package scan

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

// doctorSnapshot is a complete, diagnostic-free snapshot: the healthy
// baseline every degradation case starts from.
func doctorSnapshot() analyze.Snapshot {
	return analyze.Snapshot{
		Root: ".",
		Files: []analyze.File{
			{Path: "cmd/app/main.go", Language: languageGo},
			{Path: "internal/service/service.go", Language: languageGo},
		},
		Symbols: []analyze.Symbol{
			{Name: "main", Location: analyze.Location{Path: "cmd/app/main.go"}},
			{Name: "Run", Location: analyze.Location{Path: "internal/service/service.go"}},
		},
		Edges:  []analyze.Edge{{Kind: "calls"}},
		Status: analyze.Status{Complete: true},
	}
}

func TestDoctorCountsAndHealthyBaseline(t *testing.T) {
	t.Parallel()
	// A plain temp directory is never a git work tree, so this case also
	// pins the deterministic no-git degradation path end to end.
	root := t.TempDir()
	report := Doctor(context.Background(), root, doctorSnapshot(), "defaults", "")
	if report.Schema != DoctorSchema {
		t.Fatalf("schema = %q, want %q", report.Schema, DoctorSchema)
	}
	if report.Status != doctorStatusDegraded {
		t.Fatalf("status = %q, want degraded without git", report.Status)
	}
	health := report.Analysis
	if health.Files != 2 || health.Symbols != 2 || health.Edges != 1 || health.Diagnostics != 0 || len(health.DiagnosticDetails) != 0 || !health.Complete {
		t.Fatalf("analysis = %+v", health)
	}
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "diagnostic_details") {
		t.Fatalf("healthy analysis must omit diagnostic details: %s", encoded)
	}
	if report.Config.Source != "defaults" || report.Config.Path != "" {
		t.Fatalf("config = %+v, want defaults with no path", report.Config)
	}
	if !report.Git.Available {
		if !slices.Contains(report.Warnings, "git is unavailable or the root is not a work tree; hygiene and changes packets degrade to snapshot-derived evidence") {
			t.Fatalf("missing git warning: %v", report.Warnings)
		}
	} else if report.Status != "ok" || len(report.Warnings) != 0 {
		t.Fatalf("with git the report must be ok with no warnings: %q %v", report.Status, report.Warnings)
	}
}

func TestDoctorDegradationsAndWarningOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	snap := doctorSnapshot()
	snap.Status.Complete = false
	snap.Status.Limits = []string{"max_files"}
	snap.Diagnostics = []analyze.Diagnostic{
		{Level: "error", Message: "parse failed", Location: &analyze.Location{Path: "broken.go", Line: 7}},
		{Level: "error", Message: "parse failed again"},
	}
	report := Doctor(context.Background(), root, snap, "user", "/home/dev/.config/pan/config.yaml")
	if report.Status != doctorStatusDegraded {
		t.Fatalf("status = %q, want degraded", report.Status)
	}
	if report.Config.Source != "user" || report.Config.Path != "/home/dev/.config/pan/config.yaml" {
		t.Fatalf("config = %+v", report.Config)
	}
	if !slices.Equal(report.Analysis.Limits, []string{"max_files"}) {
		t.Fatalf("limits = %v", report.Analysis.Limits)
	}
	if len(report.Analysis.DiagnosticDetails) != 2 {
		t.Fatalf("diagnostic details = %#v", report.Analysis.DiagnosticDetails)
	}
	detail := report.Analysis.DiagnosticDetails[0]
	if detail.Level != "error" || detail.Message != "parse failed" || detail.Location == nil || detail.Location.Path != "broken.go" || detail.Location.Line != 7 {
		t.Fatalf("first diagnostic detail = %#v", detail)
	}
	snap.Diagnostics[0].Message = "mutated after report"
	if report.Analysis.DiagnosticDetails[0].Message != "parse failed" {
		t.Fatalf("diagnostic details alias snapshot: %#v", report.Analysis.DiagnosticDetails)
	}
	want := []string{
		"2 analysis diagnostics recorded; inspect parse failures before trusting symbol evidence",
		"analysis truncated by configured bounds; widen max_files, max_file_bytes, max_total_bytes, or max_nodes",
	}
	if !report.Git.Available {
		want = append(want, "git is unavailable or the root is not a work tree; hygiene and changes packets degrade to snapshot-derived evidence")
	}
	if !slices.Equal(report.Warnings, want) {
		t.Fatalf("warnings = %v, want %v (sorted, unique)", report.Warnings, want)
	}
}

func TestDoctorInfoDiagnosticsDoNotWarnOrDegrade(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	snap := doctorSnapshot()
	snap.Diagnostics = []analyze.Diagnostic{
		{Level: "info", Message: "snapshot cache fallback: missing_cache"},
		{Level: "info", Message: "3 additional diagnostics suppressed"},
	}
	report := Doctor(context.Background(), root, snap, "defaults", "")
	if report.Analysis.Diagnostics != 2 || len(report.Analysis.DiagnosticDetails) != 2 {
		t.Fatalf("info diagnostics must retain raw count and details: %+v", report.Analysis)
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "inspect parse failures") {
			t.Fatalf("info-only diagnostics must not add the parse-failure warning: %v", report.Warnings)
		}
	}
	if report.Git.Available {
		if report.Status != doctorStatusOK || len(report.Warnings) != 0 {
			t.Fatalf("with git an info-only report must stay ok with no warnings: %q %v", report.Status, report.Warnings)
		}
	} else if report.Status != doctorStatusDegraded || len(report.Warnings) != 1 || report.Warnings[0] != "git is unavailable or the root is not a work tree; hygiene and changes packets degrade to snapshot-derived evidence" {
		t.Fatalf("without git the only degradation must be the git warning: %q %v", report.Status, report.Warnings)
	}
}

func TestSortedUniqueWarnings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		warnings []string
		want     []string
	}{
		{name: "empty renders nil", warnings: []string{}, want: nil},
		{name: "duplicates collapse", warnings: []string{"b", "a", "b"}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sortedUniqueWarnings(tt.warnings); !slices.Equal(got, tt.want) {
				t.Fatalf("sortedUniqueWarnings(%v) = %v, want %v", tt.warnings, got, tt.want)
			}
		})
	}
}
