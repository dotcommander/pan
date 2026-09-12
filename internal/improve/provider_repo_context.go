package improve

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/codemap"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/repo"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

const providerReviewChangeDays = 30

// repoContextBrief is the source-oriented projection used by repo_context
// brief. It keeps command, Git, instruction, map, and ownership evidence
// separate so callers do not mistake it for the audit read queue.
type repoContextBrief struct {
	Action    string                    `json:"action"`
	Boot      repoContextBoot           `json:"boot"`
	Digest    scan.OverviewReport       `json:"digest"`
	Verify    repoContextVerification   `json:"verify"`
	Git       repoContextGitState       `json:"git"`
	Rules     []string                  `json:"rules,omitempty"`
	Map       codemap.Result            `json:"map"`
	Ownership []repoContextOwnershipRow `json:"ownership"`
}

type repoContextBoot struct {
	Root   string         `json:"root"`
	Status analyze.Status `json:"status"`
}

type repoContextVerification struct {
	Build string `json:"build"`
	Test  string `json:"test"`
	Vet   string `json:"vet,omitempty"`
}

type repoContextGitState struct {
	Branch string   `json:"branch"`
	Dirty  []string `json:"dirty,omitempty"`
	Recent []string `json:"recent,omitempty"`
}

// repoContextOwnershipRow reports structural ownership evidence only; it
// does not infer people or teams from paths.
type repoContextOwnershipRow struct {
	Path    string `json:"path"`
	Package string `json:"package,omitempty"`
	Symbols int    `json:"symbols"`
	Score   int    `json:"score"`
	Tag     string `json:"tag,omitempty"`
}

func (r *providerReader) repoContextBrief(ctx context.Context, snap analyze.Snapshot, ranked []ranking.RankedFile, args map[string]any) (any, error) {
	rules, _, err := repo.Instructions(snap.Root, repo.Scope{Exclude: r.excludes()})
	if err != nil {
		return nil, fmt.Errorf("discover repository rules: %w", err)
	}
	rules = providerContextAllowedPaths(rules, r.exclude)
	limit := boundedProviderInt(args, "limit", 12, 1, 40)
	return repoContextBrief{
		Action: "brief",
		Boot:   repoContextBoot{Root: snap.Root, Status: snap.Status},
		Digest: scan.Overview(snap),
		Verify: providerContextVerifyCommands(snap.Root),
		Git:    providerContextGitStateFor(ctx, snap.Root),
		Rules:  rules,
		Map: codemap.Build(ranked, codemap.Options{
			Mode:   codemap.ModeEnriched,
			Tokens: boundedProviderInt(args, "tokens", 4096, 512, 8192),
			Root:   snap.Root,
			Edges:  snap.Edges,
		}),
		Ownership: providerContextOwnership(ranked, limit),
	}, nil
}

// repoContextAudit matches the bounded local review-brief projection. The
// provider's filtered snapshot remains the only source for scan evidence.
func (r *providerReader) repoContextAudit(ctx context.Context, snap analyze.Snapshot, args map[string]any) (any, error) {
	limit := boundedProviderInt(args, "limit", 12, 1, 40)
	risks, err := scan.Risk(ctx, snap, limit)
	if err != nil {
		return nil, err
	}
	surface, err := scan.Surface(ctx, snap, limit)
	if err != nil {
		return nil, err
	}
	effects, err := scan.EffectsWithOptions(ctx, snap, scan.EffectsOptions{Limit: limit})
	if err != nil {
		return nil, err
	}
	report := review.Compose(review.Packets{
		Overview: scan.Overview(snap),
		Risks:    risks,
		Surface:  surface,
		Effects:  effects,
		Hygiene:  scan.Hygiene(ctx, snap.Root, snap),
		Changes:  scan.Changes(ctx, snap.Root, providerReviewChangeDays, limit, time.Time{}),
		Paths:    review.ReportPaths(snap.Root, snap.Files),
	}, limit)
	return map[string]any{
		"action":       "audit",
		"overview":     report.Overview,
		"read_queue":   report.ReadQueue,
		"review_gates": []string{"go test ./...", "go vet ./..."},
		"caveat":       "Deterministic local evidence; inspect ranked files before changing them.",
	}, nil
}

func providerContextOwnership(ranked []ranking.RankedFile, limit int) []repoContextOwnershipRow {
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	rows := make([]repoContextOwnershipRow, 0, len(ranked))
	for _, file := range ranked {
		rows = append(rows, repoContextOwnershipRow{
			Path:    file.Path,
			Package: file.Package,
			Symbols: len(file.Symbols),
			Score:   file.Score,
			Tag:     file.Tag,
		})
	}
	return rows
}

func providerContextAllowedPaths(paths []string, excluded map[string]bool) []string {
	allowed := make([]string, 0, len(paths))
	for _, path := range paths {
		if !deniedProviderPath(path) && !excluded[path] {
			allowed = append(allowed, path)
		}
	}
	return allowed
}

func providerContextVerifyCommands(root string) repoContextVerification {
	if providerContextFileExists(root, "go.mod") {
		return repoContextVerification{Build: "go build ./...", Test: "go test ./...", Vet: "go vet ./..."}
	}
	if providerContextFileExists(root, "package.json") {
		return repoContextVerification{Build: "npm run build", Test: "npm test"}
	}
	return repoContextVerification{Build: "(unknown)", Test: "(unknown)"}
}

func providerContextGitStateFor(ctx context.Context, root string) repoContextGitState {
	state := repoContextGitState{Branch: providerContextGitOutput(ctx, "-C", root, "branch", "--show-current")}
	if state.Branch == "" {
		state.Branch = "(none)"
	}
	state.Dirty = providerContextGitLines(ctx, "-C", root, "status", "--short")
	state.Recent = providerContextGitLines(ctx, "-C", root, "log", "-3", "--format=%s")
	return state
}

func providerContextFileExists(root, name string) bool {
	info, err := os.Stat(filepath.Join(root, name))
	return err == nil && !info.IsDir()
}

func providerContextGitOutput(ctx context.Context, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// #nosec G204 -- the executable is fixed and arguments are generated locally.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func providerContextGitLines(ctx context.Context, args ...string) []string {
	output := providerContextGitOutput(ctx, args...)
	if output == "" {
		return nil
	}
	return strings.Split(output, "\n")
}
