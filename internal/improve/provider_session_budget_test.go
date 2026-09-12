package improve

import (
	"context"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/provider"
)

func TestProviderExplorationBudgetCountsCallsAcrossResponses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "demo.go", "package demo\n")
	call := provider.ToolCall{ID: "read", Function: provider.FunctionCall{Name: providerReadFile, Arguments: `{"path":"demo.go"}`}}
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "across responses", true: "single response"}[batch], func(t *testing.T) {
			t.Parallel()
			responses := []provider.Response{toolResponse(call), toolResponse(call, call)}
			if batch {
				responses = []provider.Response{toolResponse(call, call, call)}
			}
			client := &scriptedCompletion{responses: responses}
			reader, err := newProviderReader(root, nil, 1024)
			if err != nil {
				t.Fatal(err)
			}
			proposer := ProviderProposer{Client: client, Settings: ProviderSettings{AgentMaxToolIterations: 2}}
			_, trace, err := proposer.proposeRepositoryAttempt(providerAttemptInput{ctx: context.Background(), reader: reader})
			if err == nil || !strings.Contains(err.Error(), "exploration tool calls") || len(trace.Tools) != 2 {
				t.Fatalf("tool count=%d error=%v; want two dispatched calls and budget refusal", len(trace.Tools), err)
			}
		})
	}
}
