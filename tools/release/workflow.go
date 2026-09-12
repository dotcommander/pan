package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sourceRepository = "dotcommander/pan"
	tapRepository    = "dotcommander/homebrew-tap"
	tapName          = "dotcommander/tap"
	formulaName      = "dotcommander/tap/pan"
)

var releasePhaseNames = []string{
	"source-preflight",
	"source-prepare",
	"source-verify",
	"source-commit",
	"source-tag",
	"source-publish",
	"github-release",
	"archive-verify",
	"tap-preflight",
	"tap-prepare",
	"tap-verify",
	"tap-commit",
	"tap-publish",
	"final-readback",
}

type options struct {
	version string
	publish bool
	resume  bool
	help    bool
}

type releaseState struct {
	Version          string            `json:"version"`
	Tag              string            `json:"tag"`
	SourceRoot       string            `json:"source_root"`
	PreviousVersion  string            `json:"previous_version,omitempty"`
	SourceStart      string            `json:"source_start,omitempty"`
	SourceRemoteBase string            `json:"source_remote_base,omitempty"`
	ReleaseCommit    string            `json:"release_commit,omitempty"`
	ArchiveSHA256    string            `json:"archive_sha256,omitempty"`
	TapRoot          string            `json:"tap_root,omitempty"`
	TapRemoteBase    string            `json:"tap_remote_base,omitempty"`
	TapCommit        string            `json:"tap_commit,omitempty"`
	Completed        map[string]string `json:"completed"`
}

type releaseWorkflow struct {
	proc      *process
	root      string
	version   string
	tag       string
	publish   bool
	resume    bool
	statePath string
	state     releaseState
}

type releasePhase struct {
	name string
	run  func(context.Context) error
}

func parseOptions(args []string) (options, error) {
	var opts options
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			opts.help = true
		case "--publish":
			opts.publish = true
		case "--resume":
			opts.resume = true
		default:
			if strings.HasPrefix(arg, "-") {
				return options{}, fmt.Errorf("unknown option %q", arg)
			}
			if opts.version != "" {
				return options{}, fmt.Errorf("multiple versions supplied: %q and %q", opts.version, arg)
			}
			opts.version = arg
		}
	}
	if opts.help {
		return opts, nil
	}
	if opts.version == "" {
		return options{}, errors.New("version is required")
	}
	version, err := normalizeVersion(opts.version)
	if err != nil {
		return options{}, err
	}
	opts.version = version
	if opts.resume && !opts.publish {
		return options{}, errors.New("--resume requires --publish")
	}
	return opts, nil
}

func newReleaseWorkflow(ctx context.Context, proc *process, root string, opts options) (*releaseWorkflow, error) {
	gitDir, err := proc.capture(ctx, root, nil, "git", "rev-parse", "--git-dir")
	if err != nil {
		return nil, fmt.Errorf("resolve git directory: %w", err)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	return &releaseWorkflow{
		proc:      proc,
		root:      root,
		version:   opts.version,
		tag:       "v" + opts.version,
		publish:   opts.publish,
		resume:    opts.resume,
		statePath: filepath.Join(gitDir, "pan-release", "v"+opts.version+".json"),
	}, nil
}

func (w *releaseWorkflow) run(ctx context.Context) error {
	if !w.publish {
		return w.dryRun(ctx)
	}
	if err := w.loadOrCreateState(); err != nil {
		return err
	}

	phases := []releasePhase{
		{"source-preflight", w.sourcePreflight},
		{"source-prepare", w.sourcePrepare},
		{"source-verify", w.sourceVerify},
		{"source-commit", w.sourceCommit},
		{"source-tag", w.sourceTag},
		{"source-publish", w.sourcePublish},
		{"github-release", w.githubRelease},
		{"archive-verify", w.archiveVerify},
		{"tap-preflight", w.tapPreflight},
		{"tap-prepare", w.tapPrepare},
		{"tap-verify", w.tapVerify},
		{"tap-commit", w.tapCommit},
		{"tap-publish", w.tapPublish},
		{"final-readback", w.finalReadback},
	}
	for _, phase := range phases {
		if _, complete := w.state.Completed[phase.name]; complete {
			fmt.Fprintf(w.proc.stdout, "skip %-18s (receipt present)\n", phase.name)
			continue
		}
		fmt.Fprintf(w.proc.stdout, "==> %s\n", phase.name)
		if err := phase.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", phase.name, err)
		}
		w.state.Completed[phase.name] = time.Now().UTC().Format(time.RFC3339)
		if err := writeJSONAtomic(w.statePath, w.state); err != nil {
			return fmt.Errorf("persist %s receipt: %w", phase.name, err)
		}
	}

	fmt.Fprintf(w.proc.stdout, "released %s at %s (archive sha256 %s, tap %s)\n",
		w.tag, shortOID(w.state.ReleaseCommit), w.state.ArchiveSHA256, shortOID(w.state.TapCommit))
	return nil
}

func (w *releaseWorkflow) dryRun(ctx context.Context) error {
	if err := requireTools("git", "gh", "brew", "just"); err != nil {
		return err
	}
	current, err := w.inspectSource(ctx, false)
	if err != nil {
		return err
	}
	if compareVersions(w.version, current) < 0 {
		return fmt.Errorf("target version %s cannot be older than binaryVersion %s", w.version, current)
	}
	if _, err := w.inspectTap(ctx, false); err != nil {
		return err
	}

	fmt.Fprintf(w.proc.stdout, "dry-run release %s from binaryVersion %s\n", w.tag, current)
	for _, name := range releasePhaseNames {
		fmt.Fprintf(w.proc.stdout, "  - %s\n", name)
	}
	fmt.Fprintln(w.proc.stdout, "no files, refs, releases, taps, or installations were changed")
	return nil
}

func (w *releaseWorkflow) loadOrCreateState() error {
	if w.resume {
		data, err := os.ReadFile(w.statePath)
		if err != nil {
			return fmt.Errorf("read resume receipt %s: %w", w.statePath, err)
		}
		if err := json.Unmarshal(data, &w.state); err != nil {
			return fmt.Errorf("parse resume receipt: %w", err)
		}
		if w.state.Version != w.version || w.state.SourceRoot != w.root {
			return errors.New("resume receipt does not match the requested version and repository")
		}
		if w.state.Completed == nil {
			w.state.Completed = make(map[string]string)
		}
		return nil
	}
	if _, err := os.Stat(w.statePath); err == nil {
		return fmt.Errorf("release receipt already exists at %s; use --resume", w.statePath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect release receipt: %w", err)
	}
	w.state = releaseState{
		Version:    w.version,
		Tag:        w.tag,
		SourceRoot: w.root,
		Completed:  make(map[string]string),
	}
	if err := writeJSONAtomic(w.statePath, w.state); err != nil {
		return fmt.Errorf("create release receipt: %w", err)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

func shortOID(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}
