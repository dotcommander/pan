package improve

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestProviderRepoContextBriefAndAuditAreDistinctFilteredProjections(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", fixtureGoMod)
	writeFile(t, root, "AGENTS.md", "# rules\n")
	writeFile(t, root, "cmd/main.go", "package main\n\nfunc main() { run() }\nfunc run() {}\n")
	writeFile(t, root, "internal/private.go", "package internal\n\nfunc Private() {}\n")

	reader, err := newProviderReader(root, []string{"internal/private.go"}, 16000)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Exclude = append(cfg.Exclude, reader.excludes()...)
	snap, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	snap = reader.filterSnapshot(snap)
	ranked := ranking.Rank(snap, "", ranking.Options{})
	args := map[string]any{"limit": 4, "tokens": 512}

	brief, err := reader.repoContextBrief(context.Background(), snap, ranked, args)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := reader.repoContextAudit(context.Background(), snap, args)
	if err != nil {
		t.Fatal(err)
	}
	briefJSON := marshalProviderContext(t, brief)
	auditJSON := marshalProviderContext(t, audit)
	for _, field := range []string{"\"boot\"", "\"digest\"", "\"verify\"", "\"git\"", "\"rules\"", "\"map\"", "\"ownership\""} {
		if !strings.Contains(briefJSON, field) {
			t.Errorf("brief missing %s: %s", field, briefJSON)
		}
	}
	for _, field := range []string{"\"overview\"", "\"read_queue\"", "\"review_gates\""} {
		if !strings.Contains(auditJSON, field) {
			t.Errorf("audit missing %s: %s", field, auditJSON)
		}
	}
	if strings.Contains(briefJSON, `"path":"internal/private.go"`) || strings.Contains(auditJSON, `"path":"internal/private.go"`) {
		t.Fatalf("excluded path leaked into projection\nbrief: %s\naudit: %s", briefJSON, auditJSON)
	}
	if !strings.Contains(briefJSON, "internal/private.go (excluded)") {
		t.Fatalf("brief omitted the bounded exclusion receipt: %s", briefJSON)
	}
	if strings.Contains(briefJSON, "\"read_queue\"") || strings.Contains(auditJSON, "\"ownership\"") {
		t.Fatalf("brief and audit projections were not distinct\nbrief: %s\naudit: %s", briefJSON, auditJSON)
	}
}

func TestProviderContextGitStatePreservesStatusColumns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", fixtureGoMod)
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "go.mod"},
		{"-c", "user.name=Pan Test", "-c", "user.email=pan@example.invalid", "commit", "-qm", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	writeFile(t, root, "go.mod", fixtureGoMod+"\n// dirty\n")

	state := providerContextGitStateFor(context.Background(), root)

	if len(state.Dirty) != 1 {
		t.Fatalf("dirty entries = %v, want exactly one", state.Dirty)
	}
	if state.Dirty[0] != " M go.mod" {
		t.Fatalf("first dirty entry = %q, want %q with the leading unstaged-status column preserved", state.Dirty[0], " M go.mod")
	}
}

func marshalProviderContext(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
