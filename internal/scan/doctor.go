package scan

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
)

// DoctorSchema is stamped on every doctor report.
const DoctorSchema = "pan.doctor/v1"

// Bounds on the git probe subprocess.
const (
	doctorGitTimeout = 5 * time.Second
)

// Doctor status values.
const (
	doctorStatusOK       = "ok"
	doctorStatusDegraded = "degraded"
)

// DoctorReport is the deterministic analyzer health check: configuration
// source, git availability, and bounded snapshot health. It performs local
// checks only and never contacts a provider or mutates the repository.
type DoctorReport struct {
	Schema   string          `json:"schema"`
	Status   string          `json:"status"` // ok | degraded
	Config   DoctorConfig    `json:"config"`
	Git      DoctorGit       `json:"git"`
	Analysis DoctorHealth    `json:"analysis"`
	Warnings []string        `json:"warnings"`
	Provider *ProviderHealth `json:"provider,omitempty"`
}

// DoctorConfig reports where the effective configuration came from.
type DoctorConfig struct {
	Source string `json:"source"` // user | defaults
	Path   string `json:"path,omitempty"`
}

// DoctorGit reports whether git evidence is available for the repository.
type DoctorGit struct {
	Available bool `json:"available"`
}

// DoctorHealth summarizes one bounded analysis pass.
type DoctorHealth struct {
	Files             int                  `json:"files"`
	Symbols           int                  `json:"symbols"`
	Edges             int                  `json:"edges"`
	Diagnostics       int                  `json:"diagnostics"`
	DiagnosticDetails []analyze.Diagnostic `json:"diagnostic_details,omitempty"`
	Complete          bool                 `json:"complete"`
	Limits            []string             `json:"limits,omitempty"`
}

// Doctor derives the health report from one finalized snapshot plus the
// effective configuration source. configSource must be "user" or "defaults";
// configPath is the user configuration path when one applies.
func Doctor(ctx context.Context, root string, snap analyze.Snapshot, configSource, configPath string) DoctorReport {
	report := DoctorReport{
		Schema: DoctorSchema,
		Config: DoctorConfig{Source: configSource, Path: configPath},
		Git:    DoctorGit{Available: gitAvailable(ctx, root)},
		Analysis: DoctorHealth{
			Files:             len(snap.Files),
			Symbols:           len(snap.Symbols),
			Edges:             len(snap.Edges),
			Diagnostics:       len(snap.Diagnostics),
			DiagnosticDetails: slices.Clone(snap.Diagnostics),
			Complete:          snap.Status.Complete,
			Limits:            snap.Status.Limits,
		},
		Warnings: []string{},
	}
	nonInfoDiagnostics := countNonInfoDiagnostics(snap.Diagnostics)
	if !report.Analysis.Complete {
		report.Warnings = append(report.Warnings, "analysis truncated by configured bounds; widen max_files, max_file_bytes, max_total_bytes, or max_nodes")
	}
	if nonInfoDiagnostics > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d analysis diagnostics recorded; inspect parse failures before trusting symbol evidence", nonInfoDiagnostics))
	}
	if !report.Git.Available {
		report.Warnings = append(report.Warnings, "git is unavailable or the root is not a work tree; hygiene and changes packets degrade to snapshot-derived evidence")
	}
	report.Warnings = sortedUniqueWarnings(report.Warnings)
	degraded := !report.Analysis.Complete || nonInfoDiagnostics > 0 || !report.Git.Available
	if degraded {
		report.Status = doctorStatusDegraded
	} else {
		report.Status = doctorStatusOK
	}
	return report
}

// countNonInfoDiagnostics counts diagnostics that are not advisory info-level
// notes (cache fallback, suppressed-diagnostic summaries). Only non-info
// diagnostics indicate parse or semantic failures, so they alone gate the
// parse-failure warning and the degraded status; the report still retains
// the raw diagnostic count and details.
func countNonInfoDiagnostics(diags []analyze.Diagnostic) int {
	count := 0
	for _, diag := range diags {
		if diag.Level != "info" {
			count++
		}
	}
	return count
}

// gitAvailable probes git with a bounded, read-only rev-parse.
func gitAvailable(ctx context.Context, root string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, doctorGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return false
	}
	return strings.TrimSpace(out.String()) == "true"
}

func sortedUniqueWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return nil
	}
	out := slices.Clone(warnings)
	slices.Sort(out)
	return slices.Compact(out)
}
