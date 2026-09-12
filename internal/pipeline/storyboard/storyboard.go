// Package storyboard converts scanned Pan specs into a compact,
// deterministic lifecycle view for terminal and JSON output.
package storyboard

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

const maxSnippetLen = 120

// Storyboard is the JSON/text view model rendered by the storyboard command.
type Storyboard struct {
	ProjectName     string        `json:"project_name"`
	Entrypoint      string        `json:"entrypoint,omitempty"`
	Prelude         []Phase       `json:"prelude,omitempty"`
	SharedPipelines []Pipeline    `json:"shared_pipelines,omitempty"`
	CommandLanes    []CommandLane `json:"command_lanes,omitempty"`
	Stores          []Store       `json:"stores,omitempty"`
	Coverage        *Coverage     `json:"coverage,omitempty"`
	ScanWarning     string        `json:"scan_warning,omitempty"`

	// Command surface and drift data absorbed from the former `map` command.
	Commands       []Command       `json:"commands,omitempty"`
	CommandHelp    string          `json:"command_help,omitempty"`
	ReviewFindings []ReviewFinding `json:"review_findings,omitempty"`
	Diffs          []DiffItem      `json:"diffs,omitempty"`
	CommandDrift   []string        `json:"command_drift,omitempty"`
}

// CommandLane groups phases owned by one CLI command.
type CommandLane struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	PipelineRefs []PipelineRef `json:"pipeline_refs,omitempty"`
	Phases       []Phase       `json:"phases,omitempty"`
}

// Pipeline is a reusable phase sequence shared by multiple command lanes.
type Pipeline struct {
	Name   string         `json:"name"`
	Phases []Phase        `json:"phases,omitempty"`
	Lanes  []PipelineLane `json:"lanes,omitempty"`
}

// PipelineLane records where a shared pipeline is referenced.
type PipelineLane struct {
	Command    string `json:"command"`
	PhaseCount int    `json:"phase_count"`
}

// PipelineRef marks the shared pipeline prefix used by a command lane.
type PipelineRef struct {
	Name       string `json:"name"`
	PhaseCount int    `json:"phase_count"`
}

// Phase is a compact phase summary.
type Phase struct {
	Ordinal        int      `json:"ordinal"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind,omitempty"`
	Goal           string   `json:"goal,omitempty"`
	Files          []string `json:"files,omitempty"`
	Stages         []Stage  `json:"stages,omitempty"`
	Sources        []string `json:"sources,omitempty"`
	TruncatedCount int      `json:"truncated_count,omitempty"`
}

// Stage is a display-ready stage with optional source provenance.
type Stage struct {
	Label       string `json:"label"`
	Role        string `json:"role,omitempty"`
	SourceFile  string `json:"source_file,omitempty"`
	SourceLine  int    `json:"source_line,omitempty"`
	CodeSnippet string `json:"code_snippet,omitempty"`
}

// Store is a persistent state artifact detected by scan.
type Store struct {
	Name    string   `json:"name"`
	Writers []Writer `json:"writers,omitempty"`
}

// Writer links a stage to a store.
type Writer struct {
	Stage  string `json:"stage"`
	Access string `json:"access,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Coverage captures scan representation metrics.
type Coverage struct {
	Represented int      `json:"represented"`
	Total       int      `json:"total"`
	Missing     []string `json:"missing,omitempty"`
	Warning     string   `json:"warning,omitempty"`
}

// Flag describes one CLI flag harvested from the target's command help.
type Flag struct {
	Name      string `json:"name"`
	Short     string `json:"short,omitempty"`
	Usage     string `json:"usage,omitempty"`
	Default   string `json:"default,omitempty"`
	Inherited bool   `json:"inherited,omitempty"`
}

// Command describes one Cobra command harvested from the target's help output.
type Command struct {
	Path           string `json:"path"`
	Use            string `json:"use,omitempty"`
	Short          string `json:"short,omitempty"`
	Long           string `json:"long,omitempty"`
	Runnable       bool   `json:"runnable,omitempty"`
	HasSubcommands bool   `json:"has_subcommands,omitempty"`
	Signature      string `json:"signature,omitempty"`
	Flags          []Flag `json:"flags,omitempty"`
}

// ReviewFinding is a drift finding flattened from the review engine.
type ReviewFinding struct {
	Severity string   `json:"severity"`
	Check    string   `json:"check,omitempty"`
	Phase    string   `json:"phase,omitempty"`
	Message  string   `json:"message,omitempty"`
	Count    int      `json:"count,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// DiffItem describes drift between a comparison spec and the live scan.
type DiffItem struct {
	Area     string   `json:"area"`
	Status   string   `json:"status,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

type sourceContext struct {
	sourceRoot string
}

// Build converts a scanned spec into a storyboard. sourceRoot may be empty; in
// that case source snippets are omitted but file and line provenance remains.
func Build(s *spec.Spec, sourceRoot, scanWarning string) Storyboard {
	sources := sourceContext{sourceRoot: sourceRoot}

	out := Storyboard{
		ProjectName: projectName(s.Title, sourceRoot),
		Entrypoint:  s.Breadcrumb,
		Prelude:     sources.phasesFromSpec(s.Phases),
		Stores:      storesFromSpec(s.Stores),
		Coverage:    coverageFromSpec(s.Coverage),
		ScanWarning: scanWarning,
	}

	commands := append([]spec.Command(nil), s.Commands...)
	sort.SliceStable(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})
	out.CommandLanes = make([]CommandLane, 0, len(commands))
	for _, cmd := range commands {
		out.CommandLanes = append(out.CommandLanes, CommandLane{
			Name:        cmd.Name,
			Description: cmd.Description,
			Phases:      sources.phasesFromSpec(cmd.Phases),
		})
	}
	annotateSharedPipelines(&out)

	if out.Coverage != nil && out.Coverage.Warning != "" && out.ScanWarning == "" {
		out.ScanWarning = out.Coverage.Warning
	}

	return out
}

func projectName(title, sourceRoot string) string {
	if title != "" {
		return title
	}
	if sourceRoot != "" {
		return filepath.Base(filepath.Clean(sourceRoot))
	}
	return "Repository"
}

// RenderJSON returns stable, indented JSON for tests and downstream tooling.
func RenderJSON(sb Storyboard) ([]byte, error) {
	data, err := json.MarshalIndent(sb, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
