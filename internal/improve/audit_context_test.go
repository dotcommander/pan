package improve

import (
	"context"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/provider"
)

func TestBuildAuditContextProjectsLocalRepositoryEvidence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "audit.go", `package audit

import "os"

func Write(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
`)
	audit := BuildAuditContext(context.Background(), root, nil)
	if audit == nil || audit.Source != improveAuditSource || !containsString(audit.EffectKinds, "filesystem-write") || !containsString(audit.RiskLanes, "data-integrity") {
		t.Fatalf("audit = %#v", audit)
	}
}

func TestBuildAuditContextRetainsVerificationForQuietRepository(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/quiet\n\ngo 1.22\n")
	writeFile(t, root, "quiet.go", "package quiet\n")
	audit := BuildAuditContext(context.Background(), root, nil)
	if audit == nil || !containsString(audit.ReviewVerify, "go test ./...") {
		t.Fatalf("audit = %#v", audit)
	}
}

func TestProviderRepositoryIncludesAuditContextInPromptAndProposal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "audit.go", `package audit

import "os"

func dead(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
`)
	fake := &scriptedCompletion{responses: []provider.Response{
		toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: providerDeletions, Arguments: `{"rationale":"unused","deletions":[{"file_path":"audit.go","symbols":["dead"]}]}`}}),
	}}
	proposal, err := (ProviderProposer{Client: fake, Settings: ProviderSettings{RepoPath: root, AgentMaxToolIterations: 1}}).ProposeRepository(context.Background(), "cleanup", CandidatePacket{})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Audit == nil || !containsString(proposal.Audit.EffectKinds, "filesystem-write") || len(fake.requests) != 1 || !strings.Contains(fake.requests[0].Messages[1].Content, "=== REPOSITORY AUDIT CONTEXT ===") {
		t.Fatalf("proposal=%#v request=%#v", proposal, fake.requests)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
