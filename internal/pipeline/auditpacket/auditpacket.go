// Package auditpacket builds a bounded, deterministic audit packet from a
// rendered system map, for consumption by audit agents (JSON) and humans
// (markdown). Every field is derived from data the map already computed plus
// the running binary's build stamp and the target's git status.
package auditpacket

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/pipeline/buildinfo"
	"github.com/dotcommander/pan/internal/pipeline/storyboard"
)

// Schema identifies the audit packet contract version.
const Schema = "pan.audit/v1"

// Packet is the full audit surface for one mapped repo.
type Packet struct {
	Schema        string         `json:"schema"`
	Binary        buildinfo.Info `json:"binary"`
	Target        Target         `json:"target"`
	Commands      []Command      `json:"commands"`
	Writes        []Write        `json:"writes"`
	Subprocesses  []Subprocess   `json:"subprocesses"`
	Coverage      Coverage       `json:"coverage"`
	Drift         []Drift        `json:"drift"`
	FindingsLeads []Lead         `json:"findings_leads"`
	ReproCommands []string       `json:"repro_commands"`
	Limitations   []string       `json:"limitations,omitempty"`
}

// Target describes the repo under audit.
type Target struct {
	Root        string   `json:"root"`
	Project     string   `json:"project"`
	Module      string   `json:"module,omitempty"`
	GitRevision string   `json:"git_revision,omitempty"`
	GitDirty    *bool    `json:"git_dirty,omitempty"`
	Manifests   []string `json:"manifests,omitempty"`
}

// Command is one command-surface entry with help provenance.
type Command struct {
	Path           string `json:"path"`
	Short          string `json:"short,omitempty"`
	Runnable       bool   `json:"runnable"`
	HasSubcommands bool   `json:"has_subcommands"`
	HelpProvenance string `json:"help_provenance"`
	ExecutionRisk  bool   `json:"execution_risk"`
}

// Write is one persistent-state write target.
type Write struct {
	Store   string   `json:"store"`
	Writers []string `json:"writers,omitempty"`
}

// Subprocess documents a process spawn relevant to the audit.
type Subprocess struct {
	Command              string   `json:"command"`
	Source               string   `json:"source"`
	Cwd                  string   `json:"cwd,omitempty"`
	TimeoutSeconds       int      `json:"timeout_seconds,omitempty"`
	TargetControlledArgs bool     `json:"target_controlled_args"`
	Evidence             []string `json:"evidence,omitempty"`
}

// Coverage mirrors the map's represented/under-modeled accounting.
type Coverage struct {
	Represented int      `json:"represented"`
	Total       int      `json:"total"`
	Missing     []string `json:"missing,omitempty"`
}

// Drift is one spec-vs-scan divergence.
type Drift struct {
	Area     string   `json:"area"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// Lead is a deterministic audit lead with a reproduction command.
type Lead struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence,omitempty"`
	Repro    string `json:"repro,omitempty"`
}

// Options carries map-time context the Report itself does not retain.
type Options struct {
	HelpProvenance      string
	CommandHelpExecuted bool
}

// Build assembles the packet from a storyboard plus live binary/git state.
func Build(ctx context.Context, sb storyboard.Storyboard, root string, opts Options) Packet {
	p := Packet{Schema: Schema, Binary: buildinfo.Read()}
	p.Target = buildTarget(ctx, sb, root)
	p.Commands = buildCommands(sb, opts)
	p.Writes = buildWrites(sb)
	p.Subprocesses = buildSubprocesses(sb, root, opts)
	if sb.Coverage != nil {
		p.Coverage = Coverage{
			Represented: sb.Coverage.Represented,
			Total:       sb.Coverage.Total,
			Missing:     sb.Coverage.Missing,
		}
	}
	p.Drift = buildDrift(sb)
	p.FindingsLeads = buildLeads(p, opts, root)
	p.ReproCommands = collectRepro(p.FindingsLeads)
	if len(p.Subprocesses) == 0 {
		p.Limitations = append(p.Limitations,
			"no subprocess evidence found; static Go call-site inspection cannot prove the absence of dynamically invoked or non-Go subprocesses")
	}
	return p
}

func buildTarget(ctx context.Context, sb storyboard.Storyboard, root string) Target {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	t := Target{Root: abs, Project: sb.ProjectName, Manifests: listManifests(abs)}
	t.Module = readModulePath(abs)
	if rev, dirty, ok := gitStatus(ctx, abs); ok {
		t.GitRevision = rev
		d := dirty
		t.GitDirty = &d
	}
	return t
}

func readModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func listManifests(root string) []string {
	candidates := []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "justfile", "Makefile"}
	var found []string
	for _, name := range candidates {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			found = append(found, name)
		}
	}
	return found
}

func gitStatus(ctx context.Context, root string) (revision string, dirty bool, ok bool) {
	rev, err := runGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", false, false
	}
	status, err := runGit(ctx, root, "status", "--porcelain")
	if err != nil {
		return strings.TrimSpace(rev), false, true
	}
	return strings.TrimSpace(rev), strings.TrimSpace(status) != "", true
}

func runGit(ctx context.Context, root string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git")
	cmd.Dir = root
	cmd.Args = append(cmd.Args, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// RenderJSON marshals the packet as indented JSON.
func RenderJSON(p Packet) ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}
