package auditpacket

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/storyboard"
)

func hasLead(p Packet, id string) bool {
	for _, l := range p.FindingsLeads {
		if l.ID == id {
			return true
		}
	}
	return false
}

func TestReproCommandsUsePanAndQuoteTarget(t *testing.T) {
	t.Parallel()
	root := "/tmp/a 'quoted' $(target)"
	leads := buildLeads(Packet{}, Options{}, root)
	commands := collectRepro(leads)
	want := "pan flow storyboard '/tmp/a '\"'\"'quoted'\"'\"' $(target)' --audit-json"
	found := false
	for _, command := range commands {
		if !strings.HasPrefix(command, "pan ") {
			t.Errorf("reproduction uses another executable: %s", command)
		}
		if command == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing safely quoted audit command in %q", commands)
	}
}

func TestBuildPacketEnumeratesStaticExecCommandSites(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/subprocess\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	source := []byte(`package main

import (
	"context"
	"os/exec"
)

func main() {
	exec.Command("psql", "--version")
	exec.CommandContext(context.Background(), "git", "status")
}
`)
	if err := os.WriteFile(filepath.Join(root, "main.go"), source, 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	p := Build(context.Background(), storyboard.Storyboard{ProjectName: "subprocess"}, root, Options{})

	found := map[string]bool{}
	for _, s := range p.Subprocesses {
		if s.Source == "target:static" {
			found[s.Command] = true
			if len(s.Evidence) == 0 || !strings.HasPrefix(s.Evidence[0], "main.go:") {
				t.Fatalf("static subprocess missing file evidence: %#v", s)
			}
		}
	}
	for _, want := range []string{"psql", "git"} {
		if !found[want] {
			t.Fatalf("missing static subprocess %q in %#v", want, p.Subprocesses)
		}
	}
}

func TestBuildPacketLeadsAndRoundTrip(t *testing.T) {
	t.Parallel()
	sb := storyboard.Storyboard{
		ProjectName: "demo",
		Coverage:    &storyboard.Coverage{Represented: 2, Total: 3, Missing: []string{"x.go"}},
		Commands:    []storyboard.Command{{Path: "demo run", Runnable: true}},
		Stores:      []storyboard.Store{{Name: "db", Writers: []storyboard.Writer{{Stage: "save"}}}},
		Diffs:       []storyboard.DiffItem{{Area: "cmd", Status: "missing", Detail: "x"}},
	}
	p := Build(context.Background(), sb, "/tmp/demo-not-a-repo", Options{HelpProvenance: "skipped (off)"})
	if p.Schema != Schema {
		t.Fatalf("schema = %q, want %q", p.Schema, Schema)
	}
	for _, id := range []string{"generated-artifact", "under-modeled-files", "command-drift", "persistent-writes"} {
		if !hasLead(p, id) {
			t.Errorf("missing expected lead %q", id)
		}
	}
	if len(p.ReproCommands) == 0 {
		t.Errorf("expected repro commands")
	}
	data, err := RenderJSON(p)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var round Packet
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("json round-trip: %v", err)
	}
	if round.Schema != Schema {
		t.Errorf("round-trip schema = %q", round.Schema)
	}
	md := string(RenderMarkdown(p))
	if !strings.Contains(md, "Audit Leads") {
		t.Errorf("markdown missing Audit Leads section")
	}
}

func TestExecutedHelpAddsExecutionLeadAndSubprocess(t *testing.T) {
	t.Parallel()
	sb := storyboard.Storyboard{ProjectName: "demo"}
	p := Build(context.Background(), sb, "/tmp/demo-not-a-repo", Options{
		HelpProvenance:      "executed",
		CommandHelpExecuted: true,
	})
	if !hasLead(p, "command-help-execution") {
		t.Errorf("expected command-help-execution lead when executed")
	}
	found := false
	for _, s := range p.Subprocesses {
		if s.Source == "pan:command-help" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pan:command-help subprocess when executed")
	}
}
