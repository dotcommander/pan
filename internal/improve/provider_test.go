package improve

import (
	"context"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/provider"
)

type fakeCompletion struct{ response provider.Response }

func (f fakeCompletion) Complete(context.Context, provider.Request) (provider.Response, error) {
	return f.response, nil
}

type fakeProposer struct{ proposal *Proposal }

func (f fakeProposer) Propose(context.Context, string) (*Proposal, error) { return f.proposal, nil }

type repositoryProposerState struct {
	root, trace string
	exclude     []string
	packet      CandidatePacket
	fallback    bool
}

type repositoryFixtureProposer struct {
	state *repositoryProposerState
	root  string
	trace string
}

type nilRepositoryProposer struct{}

func (nilRepositoryProposer) Propose(context.Context, string) (*Proposal, error) { return nil, nil }
func (p nilRepositoryProposer) ScopeRepository(string, []string, string) RepositoryProposer {
	return p
}
func (nilRepositoryProposer) ProposeRepository(context.Context, string, CandidatePacket) (*Proposal, error) {
	return nil, nil
}

func (p repositoryFixtureProposer) Propose(context.Context, string) (*Proposal, error) {
	p.state.fallback = true
	return nil, nil
}

func (p repositoryFixtureProposer) ScopeRepository(root string, exclude []string, trace string) RepositoryProposer {
	p.root, p.trace = root, trace
	p.state.exclude = append([]string(nil), exclude...)
	return p
}

func (p repositoryFixtureProposer) ProposeRepository(_ context.Context, _ string, packet CandidatePacket) (*Proposal, error) {
	p.state.root, p.state.trace, p.state.packet = p.root, p.trace, packet
	return &Proposal{Rationale: "no justified cleanup", Checked: []string{"demo.go"}, Source: "fixture", TraceReceipt: &ProviderTrace{Status: "success"}}, nil
}

type fakeMutationRunner struct{ score float64 }

func (f fakeMutationRunner) Run(context.Context, string, []string) (float64, error) {
	return f.score, nil
}

func TestProviderProposerParsesSingleChoice(t *testing.T) {
	t.Parallel()
	p := ProviderProposer{Client: fakeCompletion{response: provider.Response{Choices: []provider.Choice{{Message: provider.Message{Content: `{"rationale":"remove dead","changes":[{"file_path":"dead.go"}]}`}}}}}}
	proposal, err := p.Propose(context.Background(), "summary")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Source != "provider" || len(proposal.Changes) != 1 {
		t.Fatalf("proposal = %+v", proposal)
	}
}

func TestProbeTraceIsSanitizedAndOptIn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "demo.go", "package demo\n")
	proposer := ProviderProposer{Client: fakeCompletion{response: provider.Response{
		ID: "completion-1", Model: "test-model", Usage: provider.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
		Choices: []provider.Choice{{FinishReason: "stop", Message: provider.Message{Content: `{"rationale":"none","changes":[]}`}}},
	}}}
	report, err := RunProbeContext(context.Background(), ProbeOptions{RepoPath: dir, Proposer: proposer, Trace: "summary"})
	if err != nil {
		t.Fatal(err)
	}
	if report.TraceReceipt == nil || report.TraceReceipt.Status != "success" || report.TraceReceipt.ResponseID != "completion-1" || report.TraceReceipt.TotalTokens != 8 {
		t.Fatalf("trace = %+v", report.TraceReceipt)
	}
	off, err := RunProbeContext(context.Background(), ProbeOptions{RepoPath: dir, Proposer: proposer, Trace: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if off.TraceReceipt != nil {
		t.Fatalf("off trace receipt = %+v", off.TraceReceipt)
	}
}

func TestRunProbeUsesInjectedProposer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "demo.go", "package demo\n\nfunc Old() {}\n")
	report, err := RunProbeContext(context.Background(), ProbeOptions{
		RepoPath: dir,
		Proposer: fakeProposer{proposal: &Proposal{Rationale: "simplify", Source: "provider", Changes: []FileChange{{FilePath: "demo.go", NewContents: "package demo\n"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Proposal.Source != "provider" || report.LinesDeleted != 2 {
		t.Fatalf("provider probe = %+v", report)
	}
}

func TestRunProbeUsesRepositoryProposerContract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "demo.go", "package demo\n\nfunc unused() {}\n")
	state := &repositoryProposerState{}
	report, err := RunProbeContext(context.Background(), ProbeOptions{
		RepoPath: dir,
		Exclude:  []string{"vendor/**"},
		Proposer: repositoryFixtureProposer{state: state},
		Trace:    "summary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.fallback || state.root != dir || state.trace != "summary" || strings.Join(state.exclude, ",") != "vendor/**" {
		t.Fatalf("repository scope = fallback %t root %q trace %q exclude %v", state.fallback, state.root, state.trace, state.exclude)
	}
	if len(state.packet.Files) == 0 || report.Outcome != string(OutcomeNoCandidate) || report.TraceReceipt == nil {
		t.Fatalf("packet/report = %+v/%+v", state.packet, report)
	}
}

func TestRunProbeRejectsNilRepositoryProposal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "demo.go", "package demo\n")
	_, err := RunProbeContext(context.Background(), ProbeOptions{RepoPath: dir, Proposer: nilRepositoryProposer{}})
	if err == nil || !strings.Contains(err.Error(), "repository proposer returned nil proposal") {
		t.Fatalf("error = %v", err)
	}
}

func TestProbeNormalizesEmptyLegacyProposalToNoCandidate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "demo.go", "package demo\n")
	report, err := RunProbeContext(context.Background(), ProbeOptions{
		RepoPath: dir,
		Proposer: fakeProposer{proposal: &Proposal{Rationale: "no justified cleanup in demo.go"}},
		Trace:    "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Outcome != string(OutcomeNoCandidate) || report.Proposal.Disposition != string(OutcomeNoCandidate) {
		t.Fatalf("report=%+v", report)
	}
}

func TestRunRefactorLiveCommitsDisposableFixture(t *testing.T) {
	t.Parallel()
	dir := newDeadCodeFixture(t)
	if _, err := InitIsolatedRepository(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	report, err := RunRefactor(context.Background(), RefactorOptions{
		RepoPath: dir, StateDir: t.TempDir(), Live: true,
		MutationGate: true, MutationFloor: 0.8, MutationRunner: fakeMutationRunner{score: 0.9},
		Proposer: fakeProposer{proposal: &Proposal{Rationale: "remove dead helper", Source: "fake", Changes: []FileChange{{FilePath: "demo.go", NewContents: `package demo

import "fmt"

func Used() string { return fmt.Sprintf("%d", helper()) }
func helper() int { return 1 }
`}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.DryRun || report.Outcome != string(OutcomeSuccess) || report.MutationScore != 0.9 {
		t.Fatalf("live report = %+v", report)
	}
	if got := readFile(t, dir, "demo.go"); got != fixtureWithDeadCode {
		t.Fatal("live refactor changed caller checkout")
	}
	branches, err := gitOutput(context.Background(), dir, "branch", "--list", "pan-improve-*")
	if err != nil || strings.TrimSpace(branches) == "" {
		t.Fatalf("live refactor attempt branch missing: %q, %v", branches, err)
	}
}
