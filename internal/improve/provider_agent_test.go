package improve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dotcommander/pan/internal/provider"
)

type scriptedCompletion struct {
	requests  []provider.Request
	responses []provider.Response
	err       error
}

type improveRoundTripFunc func(*http.Request) (*http.Response, error)

func (f improveRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f *scriptedCompletion) Complete(ctx context.Context, request provider.Request) (provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return provider.Response{}, err
	}
	f.requests = append(f.requests, request)
	if f.err != nil {
		return provider.Response{}, f.err
	}
	if len(f.responses) == 0 {
		return provider.Response{}, errors.New("unexpected provider request")
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

func toolResponse(calls ...provider.ToolCall) provider.Response {
	return provider.Response{Choices: []provider.Choice{{Message: provider.Message{ToolCalls: calls}}}}
}

func textResponse(content string) provider.Response {
	return provider.Response{Choices: []provider.Choice{{Message: provider.Message{Content: content}}}}
}

func TestProviderRepositorySuppliesSourceAndCapturesTerminalPacket(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc Keep() string { return \"ok\" }\n")
	fake := &scriptedCompletion{responses: []provider.Response{{Choices: []provider.Choice{{Message: provider.Message{ToolCalls: []provider.ToolCall{{ID: "done", Type: "function", Function: provider.FunctionCall{Name: "submit_proposal", Arguments: `{"rationale":"simplify","changes":[{"file_path":"demo.go","new_contents":"package demo\n"}],"candidate_packet":{"files":[{"path":"demo.go","reasons":["read"]}]}}`}}}}}}}}}
	proposal, err := (ProviderProposer{Client: fake, Model: "test", Settings: ProviderSettings{RepoPath: root, MaxCodebaseBytes: 4096, ProposalFormat: "wholefile"}}).ProposeRepository(context.Background(), "find a cleanup", CandidatePacket{SkippedSignals: []string{"baseline"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 1 || !strings.Contains(fake.requests[0].Messages[1].Content, "func Keep") {
		t.Fatalf("request did not carry source: %+v", fake.requests)
	}
	if proposal.CandidatePacket == nil || proposal.CandidatePacket.Files[0].Path != "demo.go" {
		t.Fatalf("candidate packet lost: %+v", proposal)
	}
	if got := readFile(t, root, "demo.go"); !strings.Contains(got, "Keep") {
		t.Fatalf("generation mutated repository: %q", got)
	}
}

func TestProviderRepositoryAcceptsFirstResponseNoCandidate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc Keep() string { return \"ok\" }\n")
	fake := &scriptedCompletion{responses: []provider.Response{toolResponse(provider.ToolCall{
		ID: "done", Type: "function", Function: provider.FunctionCall{Name: providerNoCandidate, Arguments: `{"rationale":"the supplied owner has no justified cleanup","checked":["demo.go:Keep","demo.go:Keep"],"limitations":["runtime behavior not checked"]}`},
	})}}
	packet := CandidatePacket{SkippedSignals: []string{"git_history_not_checked"}}
	proposal, err := (ProviderProposer{Client: fake, Model: "test", Settings: ProviderSettings{RepoPath: root, MaxCodebaseBytes: 4096, TraceMode: "summary"}}).ProposeRepository(context.Background(), "find a cleanup", packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 1 || proposal.Disposition != string(OutcomeNoCandidate) || len(proposal.Changes) != 0 {
		t.Fatalf("requests=%d proposal=%+v", len(fake.requests), proposal)
	}
	if proposal.TraceReceipt == nil || proposal.TraceReceipt.ResponseCount != 1 || proposal.TraceReceipt.ExplorationCallCount != 0 || proposal.TraceReceipt.AcceptedTerminalCount != 1 || proposal.TraceReceipt.TerminalTool != providerNoCandidate {
		t.Fatalf("trace receipt = %+v", proposal.TraceReceipt)
	}
	if got, want := strings.Join(proposal.Checked, ","), "demo.go:Keep"; got != want {
		t.Fatalf("checked=%q want=%q", got, want)
	}
	if got := strings.Join(proposal.Limitations, ","); got != "runtime behavior not checked,git_history_not_checked" {
		t.Fatalf("limitations=%q", got)
	}
	if got := readFile(t, root, "demo.go"); !strings.Contains(got, "func Keep") {
		t.Fatalf("no-candidate terminal mutated repository: %q", got)
	}
}

func TestProviderNoCandidateRequiresEvidence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n")
	fake := &scriptedCompletion{responses: []provider.Response{toolResponse(provider.ToolCall{
		ID: "done", Function: provider.FunctionCall{Name: providerNoCandidate, Arguments: `{"rationale":"nothing justified","checked":[]}`},
	})}}
	_, err := (ProviderProposer{Client: fake, Settings: ProviderSettings{RepoPath: root}}).ProposeRepository(context.Background(), "cleanup", CandidatePacket{})
	if err == nil || !strings.Contains(err.Error(), "missing checked evidence") {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderToolsAlwaysExposeNoCandidateTerminal(t *testing.T) {
	t.Parallel()
	for _, symbols := range []bool{false, true} {
		found := false
		for _, tool := range providerTools(symbols) {
			if tool.Function.Name == providerNoCandidate {
				found = true
			}
		}
		if !found {
			t.Fatalf("symbols=%v omitted %s", symbols, providerNoCandidate)
		}
	}
}

func TestProviderToolArraySchemasDeclareItems(t *testing.T) {
	t.Parallel()
	for _, symbols := range []bool{false, true} {
		for _, tool := range providerTools(symbols) {
			assertArrayItems(t, tool.Function.Name, tool.Function.Parameters)
		}
	}
}

func TestGeminiPayloadSerializesCompleteProviderToolSchemas(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc Keep() string { return \"ok\" }\n")
	transport := improveRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("key") != "fixture-key" {
			t.Error("Gemini API key query was not set")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		assertArrayItems(t, "gemini", payload["tools"])
		body := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"submit_no_candidate","args":{"rationale":"checked supplied source","checked":["demo.go:Keep"]}}}]},"finishReason":"STOP"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	client, err := provider.New(provider.Config{Provider: "gemini", APIKey: "fixture-key", Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := (ProviderProposer{Client: client, Model: "fixture", Settings: ProviderSettings{RepoPath: root, MaxCodebaseBytes: 4096}}).ProposeRepository(t.Context(), "inspect", CandidatePacket{})
	if err != nil || proposal.Disposition != string(OutcomeNoCandidate) {
		t.Fatalf("Gemini proposal = %+v, %v", proposal, err)
	}
}

func assertArrayItems(t *testing.T, path string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if typed[kindType] == jsonArray && typed[providerItemsKey] == nil {
			t.Errorf("%s: array schema is missing items", path)
		}
		for key, child := range typed {
			assertArrayItems(t, path+"."+key, child)
		}
	case []any:
		for _, child := range typed {
			assertArrayItems(t, path, child)
		}
	}
}

func TestProviderRepositoryRunsReadOnlyToolThenTerminalDeletion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc Keep() {}\nfunc dead() {}\n")
	fake := &scriptedCompletion{responses: []provider.Response{
		toolResponse(provider.ToolCall{ID: "read", Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"demo.go"}`}}),
		toolResponse(provider.ToolCall{ID: "done", Type: "function", Function: provider.FunctionCall{Name: "submit_deletions", Arguments: `{"rationale":"unused","deletions":[{"file_path":"demo.go","symbols":["dead"],"reasoning":"unused"}]}`}}),
	}}
	proposal, err := (ProviderProposer{Client: fake, Settings: ProviderSettings{RepoPath: root, AgentMaxToolIterations: 2}}).ProposeRepository(context.Background(), "cleanup", CandidatePacket{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 || len(fake.requests[1].Messages) < 4 {
		t.Fatalf("tool result was not returned: %+v", fake.requests)
	}
	if len(proposal.Changes) != 1 || strings.Contains(proposal.Changes[0].NewContents, "dead") {
		t.Fatalf("deletions = %+v", proposal.Changes)
	}
	if strings.Contains(readFile(t, root, "demo.go"), "func dead") == false {
		t.Fatal("deletion generation mutated source")
	}
}

func TestProviderRepositoryBoundsAndRejectsEscapes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n")
	fake := &scriptedCompletion{responses: []provider.Response{
		toolResponse(provider.ToolCall{ID: "bad", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"../outside"}`}}),
		toolResponse(provider.ToolCall{ID: "again", Function: provider.FunctionCall{Name: "list_dir", Arguments: `{}`}}),
	}}
	_, err := (ProviderProposer{Client: fake, Settings: ProviderSettings{RepoPath: root, AgentMaxToolIterations: 2}}).ProposeRepository(context.Background(), "cleanup", CandidatePacket{})
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, want iteration cap", err)
	}
	if !strings.Contains(fake.requests[1].Messages[len(fake.requests[1].Messages)-1].Content, "tool refused") {
		t.Fatal("escape refusal not returned to model")
	}
}

func TestProviderTestGeneratorUsesPrepModelAndSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc Target() int { return 1 }\n")
	writeFile(t, root, "helper.go", "package demo\n\nconst Answer = 1\n")
	fake := &scriptedCompletion{responses: []provider.Response{textResponse(`{"changes":[{"file_path":"demo_test.go","new_contents":"package demo"}]}`)}}
	changes, err := (ProviderTestGenerator{Client: fake, Model: "main", Settings: ProviderSettings{RepoPath: root, PrepFileModel: "prep", PrepPromptSourceBytes: 4096, PrepPackageContextBytes: 4096, PrepPromptTargetFuncs: 1, PrepPromptCoverageGaps: 1, PrepSystemPrompt: "fallback system"}}).Generate(context.Background(), PrepTarget{File: "demo.go", CoveragePercent: 20, Gaps: []Gap{{StartLine: 3, EndLine: 3, Statements: 1}, {StartLine: 4, EndLine: 4, Statements: 1}}}, "focus target")
	if err != nil {
		t.Fatal(err)
	}
	request := fake.requests[0]
	thinking, ok := request.ProviderOptions["thinking"].(map[string]any)
	if request.Model != "prep" || !strings.Contains(request.Messages[1].Content, "func Target") || !strings.Contains(request.Messages[1].Content, "const Answer") || !strings.Contains(request.Messages[1].Content, "TARGET FUNCTIONS") || strings.Count(request.Messages[1].Content, "statements)") != 1 || !strings.Contains(request.Messages[0].Content, "fallback system") || !strings.Contains(request.Messages[0].Content, "Thinking is disabled") || !ok || thinking["type"] != "disabled" {
		t.Fatalf("prep request = %+v", request)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestProviderRepositoryCorrectsMalformedResponseAndEscalates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc dead() {}\n")
	fake := &scriptedCompletion{responses: []provider.Response{
		textResponse(`{"rationale":"missing terminal","changes":[{"file_path":"demo.go","new_contents":"package demo"}]}`),
		toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: "submit_deletions", Arguments: `{"rationale":"unused","deletions":[{"file_path":"demo.go","symbols":["dead"]}]}`}}),
	}}
	proposal, err := (ProviderProposer{Client: fake, Model: "base", Settings: ProviderSettings{RepoPath: root, AgentMaxToolIterations: 1, MaxRetries: 1, EscalationModel: "escalated", TraceMode: "summary"}}).ProposeRepository(context.Background(), "cleanup", CandidatePacket{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 || fake.requests[1].Model != "escalated" || !strings.Contains(fake.requests[1].Messages[1].Content, "CORRECTION REQUIRED") {
		t.Fatalf("correction requests = %+v", fake.requests)
	}
	if proposal.TraceReceipt == nil || proposal.TraceReceipt.Model != "escalated" {
		t.Fatalf("trace receipt = %+v", proposal.TraceReceipt)
	}
	if proposal.TraceReceipt.ResponseCount != 2 || proposal.TraceReceipt.ExplorationCallCount != 0 || proposal.TraceReceipt.AcceptedTerminalCount != 1 || proposal.TraceReceipt.TerminalTool != providerDeletions {
		t.Fatalf("trace counters = %+v", proposal.TraceReceipt)
	}
}

func TestProviderRepositoryTraceSanitizesToolResults(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc dead() {}\n")
	fake := &scriptedCompletion{responses: []provider.Response{
		toolResponse(provider.ToolCall{ID: "read", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"demo.go"}`}}),
		toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: "submit_deletions", Arguments: `{"rationale":"unused","deletions":[{"file_path":"demo.go","symbols":["dead"]}]}`}}),
	}}
	proposal, trace, err := (ProviderProposer{Client: fake, Model: "test", Settings: ProviderSettings{RepoPath: root, AgentMaxToolIterations: 2}}).ProposeWithTrace(context.Background(), "cleanup")
	if err != nil || proposal == nil || trace.Status != "success" || trace.ResponseCount != 2 || trace.ExplorationCallCount != 1 || trace.AcceptedTerminalCount != 1 || trace.TerminalTool != providerDeletions || len(trace.Tools) != 1 || trace.Tools[0].Name != "read_file" || trace.Tools[0].ResultBytes == 0 || trace.Tools[0].Result != "" {
		t.Fatalf("proposal=%+v trace=%+v err=%v", proposal, trace, err)
	}
}

func TestProviderTraceCountsMultipleExplorationCallsInOneResponse(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n\nfunc dead() {}\n")
	fake := &scriptedCompletion{responses: []provider.Response{
		{Choices: []provider.Choice{{Message: provider.Message{ToolCalls: []provider.ToolCall{
			{ID: "read-1", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"demo.go"}`}},
			{ID: "read-2", Function: provider.FunctionCall{Name: providerSearchFiles, Arguments: `{"pattern":"dead"}`}},
		}}}}},
		toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: providerNoCandidate, Arguments: `{"rationale":"no safe change","checked":["demo.go:dead"]}`}}),
	}}
	proposal, trace, err := (ProviderProposer{Client: fake, Model: "test", Settings: ProviderSettings{RepoPath: root, AgentMaxToolIterations: 3}}).ProposeWithTrace(context.Background(), "cleanup")
	if err != nil || proposal == nil || trace.ResponseCount != 2 || trace.ExplorationCallCount != 2 || trace.AcceptedTerminalCount != 1 || len(trace.Tools) != 2 || trace.TerminalTool != providerNoCandidate {
		t.Fatalf("proposal=%+v trace=%+v err=%v", proposal, trace, err)
	}
}

func TestProviderTraceDoesNotAcceptRejectedTerminal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n")
	reader, err := newProviderReader(root, nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	trace := responseTrace(ProviderTrace{}, toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: providerNoCandidate, Arguments: `{"rationale":"missing evidence","checked":[]}`}}))
	_, _, err = (ProviderProposer{}).consumeRepositoryResponse(providerResponseInput{ctx: context.Background(), reader: reader, response: toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: providerNoCandidate, Arguments: `{"rationale":"missing evidence","checked":[]}`}}), iterations: 1, trace: &trace})
	if err == nil || trace.ResponseCount != 1 || trace.ExplorationCallCount != 0 || trace.AcceptedTerminalCount != 0 || trace.TerminalTool != "" {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
}

func TestProviderTraceJSONAlwaysEmitsEfficiencyCounts(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(ProviderTrace{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"response_count":0`, `"exploration_call_count":0`, `"accepted_terminal_count":0`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("trace JSON %s does not contain %s", encoded, field)
		}
	}
}

func TestProviderRepositoryFullTraceRedactsAndCapsToolResults(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n// API_KEY=synthetic-secret\nconst Safe = \"ok\"\n"+strings.Repeat("// padding\n", 32)+"func dead() {}\n")
	fake := &scriptedCompletion{responses: []provider.Response{
		toolResponse(provider.ToolCall{ID: "read", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"demo.go"}`}}),
		toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: "submit_deletions", Arguments: `{"rationale":"unused","deletions":[{"file_path":"demo.go","symbols":["dead"]}]}`}}),
	}}
	proposal, err := (ProviderProposer{Client: fake, Model: "test", Settings: ProviderSettings{RepoPath: root, AgentMaxToolIterations: 2, TraceMode: "full", TraceFullResultBytes: 96}}).ProposeRepository(context.Background(), "cleanup", CandidatePacket{})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.TraceReceipt == nil || len(proposal.TraceReceipt.Tools) != 1 {
		t.Fatalf("trace receipt = %+v", proposal.TraceReceipt)
	}
	result := proposal.TraceReceipt.Tools[0].Result
	if len(result) != 96 || !strings.Contains(result, "package demo") || !strings.Contains(result, "[REDACTED]") || strings.Contains(result, "synthetic-secret") {
		t.Fatalf("full trace result = %q", result)
	}
}

func TestScrubProviderTextRedactsQuotedAndBearerSecretsWithUTF8Cap(t *testing.T) {
	t.Parallel()
	value := "api_key := \"go-secret\"; {\\\"api_key\\\":\\\"escaped-secret\\\"}; {\"api_key\":\"json-secret\"}; Authorization: Bearer abc123; éé"
	got := scrubProviderText(value, len(value)-1)
	for _, secret := range []string{"go-secret", "escaped-secret", "json-secret", "abc123"} {
		if strings.Contains(got, secret) {
			t.Fatalf("scrubbed value leaks %q: %q", secret, got)
		}
	}
	if strings.Count(got, "[REDACTED]") != 4 || !utf8.ValidString(got) || len(got) > len(value)-1 {
		t.Fatalf("scrubbed value = %q", got)
	}
	if capped := scrubProviderText("plain éé", 7); capped != "plain " || !utf8.ValidString(capped) {
		t.Fatalf("UTF-8 cap = %q", capped)
	}
}

func TestProviderRepositoryOffTraceHasNoReceipt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\nfunc dead() {}\n")
	fake := &scriptedCompletion{responses: []provider.Response{
		toolResponse(provider.ToolCall{ID: "done", Function: provider.FunctionCall{Name: "submit_deletions", Arguments: `{"rationale":"unused","deletions":[{"file_path":"demo.go","symbols":["dead"]}]}`}}),
	}}
	proposal, err := (ProviderProposer{Client: fake, Settings: ProviderSettings{RepoPath: root, TraceMode: "off"}}).ProposeRepository(context.Background(), "cleanup", CandidatePacket{})
	if err != nil || proposal.TraceReceipt != nil {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}
}

func TestProviderRepositoryPropagatesCancellationAndProviderError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (ProviderProposer{Client: &scriptedCompletion{}, Settings: ProviderSettings{RepoPath: root}}).ProposeRepository(ctx, "x", CandidatePacket{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
	providerErr := errors.New("transport")
	_, err = (ProviderProposer{Client: &scriptedCompletion{err: providerErr}, Settings: ProviderSettings{RepoPath: root}}).ProposeRepository(context.Background(), "x", CandidatePacket{})
	if !errors.Is(err, providerErr) {
		t.Fatalf("provider err = %v", err)
	}
}
