package improve

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/provider"
)

func TestPrepareRefactorNoCandidateStopsBeforeApply(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeFile(t, repo, "demo.go", "package demo\n")
	run := newRefactorRun(RefactorOptions{
		RepoPath: repo,
		StateDir: t.TempDir(),
		Proposer: fakeProposer{proposal: &Proposal{Rationale: "checked demo.go; no justified change", Checked: []string{"demo.go"}}},
	})
	result, err := prepareRefactorAttempt(context.Background(), refactorPrepareInput{
		run: run, copyDir: repo, baseline: "unused", prefix: "unused", head: "unused", baselineTests: TestResult{AllPassed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.stop || !result.report.Success || result.report.Outcome != string(OutcomeNoCandidate) {
		t.Fatalf("result=%+v", result)
	}
	if result.tx != nil || result.report.Proposal.Disposition != string(OutcomeNoCandidate) {
		t.Fatalf("no-candidate result entered apply path: %+v", result)
	}
}

type proposalSequence struct {
	proposals []*Proposal
	summaries []string
}

func (p *proposalSequence) Propose(_ context.Context, summary string) (*Proposal, error) {
	p.summaries = append(p.summaries, summary)
	if len(p.proposals) == 0 {
		return nil, errors.New("unexpected proposal request")
	}
	proposal := p.proposals[0]
	p.proposals = p.proposals[1:]
	return proposal, nil
}

type errorProposer struct{ calls int }

func (p *errorProposer) Propose(context.Context, string) (*Proposal, error) {
	p.calls++
	return nil, errors.New("provider transport failed")
}

func TestRunRefactorRetriesKnownPostTestFailureAfterRollback(t *testing.T) {
	dir := newDeadCodeFixture(t)
	writeFile(t, dir, "demo_test.go", `package demo

import "testing"

func TestUsed(t *testing.T) {
	if got := Used(); got != "1" { t.Fatalf("Used() = %q", got) }
}
`)
	writeFile(t, dir, "unused.go", "package demo\n\nfunc unused() {}\n")
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	proposer := &proposalSequence{proposals: []*Proposal{
		{Rationale: "change behavior", Changes: []FileChange{{FilePath: "demo.go", NewContents: `package demo

import "fmt"

func Used() string { return fmt.Sprintf("%d", helper()) }

func helper() int { return 2 }

func deadHelper() int { return 2 }
`}}},
		{Rationale: "remove unrelated unused symbol", Changes: []FileChange{{FilePath: "unused.go", NewContents: "package demo\n"}}},
	}}
	report, err := RunRefactor(context.Background(), RefactorOptions{
		RepoPath: dir, StateDir: t.TempDir(), Proposer: proposer, MaxRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Attempts != 2 || report.Outcome != string(OutcomeSuccess) {
		t.Fatalf("report = %+v", report)
	}
	if len(proposer.summaries) != 2 || !strings.Contains(proposer.summaries[1], "rejected_outcome: post_tests") {
		t.Fatalf("corrective summaries = %#v", proposer.summaries)
	}
	if got := readFile(t, dir, "demo.go"); got != fixtureWithDeadCode {
		t.Fatal("dry-run retry mutated the source checkout")
	}
}

func TestRunRefactorProviderRetryScopesPacketAndEscalates(t *testing.T) {
	dir := newDeadCodeFixture(t)
	writeFile(t, dir, "demo_test.go", `package demo

import "testing"

func TestUsed(t *testing.T) {
	if got := Used(); got != "1" { t.Fatalf("Used() = %q", got) }
}
`)
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	first, err := json.Marshal(Proposal{Rationale: "break test", Changes: []FileChange{{FilePath: "demo.go", NewContents: "package demo\n\nfunc Used() string { return `api_key := \"go-secret\"; {\\\"api_key\\\":\\\"escaped-secret\\\"}; {\"api_key\":\"json-secret\"}; Authorization: Bearer abc123` }\n\nfunc helper() int { return 2 }\n\nfunc deadHelper() int { return 2 }\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptedCompletion{responses: []provider.Response{
		toolResponse(provider.ToolCall{ID: "first", Type: "function", Function: provider.FunctionCall{Name: "submit_proposal", Arguments: string(first)}}),
		toolResponse(provider.ToolCall{ID: "second", Type: "function", Function: provider.FunctionCall{Name: "submit_proposal", Arguments: `{"rationale":"remove dead helper","changes":[{"file_path":"demo.go","new_contents":"package demo\n\nimport \"fmt\"\n\n// Used is the exported surface.\nfunc Used() string { return fmt.Sprintf(\"%d\", helper()) }\n\nfunc helper() int { return 1 }\n"}]}`}}),
	}}
	report, err := RunRefactor(context.Background(), RefactorOptions{
		RepoPath: dir, StateDir: t.TempDir(), MaxRetries: 1,
		Proposer: ProviderProposer{
			Client: client, Model: "base", Settings: ProviderSettings{
				ProposalFormat: "wholefile", AgentMaxToolIterations: 1, EscalationModel: "strong",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Attempts != 2 || len(client.requests) != 2 {
		t.Fatalf("report=%+v requests=%+v", report, client.requests)
	}
	if client.requests[0].Model != "base" || client.requests[1].Model != "strong" {
		t.Fatalf("models = %q, %q", client.requests[0].Model, client.requests[1].Model)
	}
	if !strings.Contains(client.requests[0].Messages[1].Content, "CANDIDATE PACKET") || !strings.Contains(client.requests[1].Messages[1].Content, "rejected_outcome: post_tests") {
		t.Fatalf("provider prompts did not carry packet and feedback: %+v", client.requests)
	}
	for _, secret := range []string{"go-secret", "escaped-secret", "json-secret", "abc123"} {
		if strings.Contains(client.requests[1].Messages[1].Content, secret) {
			t.Fatalf("retry prompt leaked %q: %q", secret, client.requests[1].Messages[1].Content)
		}
	}
	if got := readFile(t, dir, "demo.go"); got != fixtureWithDeadCode {
		t.Fatal("provider retry mutated the source checkout")
	}
}

func TestRunRefactorDoesNotRetryProviderTransportFailure(t *testing.T) {
	dir := newDeadCodeFixture(t)
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	proposer := &errorProposer{}
	_, err := RunRefactor(context.Background(), RefactorOptions{
		RepoPath: dir, StateDir: t.TempDir(), Proposer: proposer, MaxRetries: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "provider transport failed") {
		t.Fatalf("err = %v", err)
	}
	if proposer.calls != 1 {
		t.Fatalf("provider calls = %d, want one unknown-outcome request", proposer.calls)
	}
}

func TestRunRefactorRefusesBelowBaselineCoverageFloor(t *testing.T) {
	dir := newDeadCodeFixture(t)
	writeFile(t, dir, "demo_test.go", `package demo

import "testing"

func TestUsed(t *testing.T) {
	if Used() != "1" { t.Fatal("unexpected value") }
}
`)
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	report, err := RunRefactor(context.Background(), RefactorOptions{
		RepoPath: dir, StateDir: t.TempDir(), MinBaselineCoverage: 0.9, DeadSymbolsFirst: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Success || report.Outcome != string(OutcomeBaselineCoverage) {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Reason, "run pan improve prep first") || report.Proposal != nil {
		t.Fatalf("baseline refusal = %+v", report)
	}
	if got := readFile(t, dir, "demo.go"); got != fixtureWithDeadCode {
		t.Fatal("baseline refusal mutated the source checkout")
	}
}
