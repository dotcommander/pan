package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

type serveTestBackend struct{}

func (serveTestBackend) AgentStatus(context.Context) (StatusSummary, error) {
	return StatusSummary{Repository: "/fixture", Schema: analyze.SchemaVersion}, nil
}
func (serveTestBackend) AgentOverview(context.Context) (scan.OverviewReport, error) {
	return scan.OverviewReport{}, nil
}
func (serveTestBackend) AgentSymbols(context.Context, string, int) ([]analyze.Symbol, error) {
	return nil, nil
}
func (serveTestBackend) AgentReport(context.Context) (review.Document, error) {
	return fixedDocument(), nil
}
func (serveTestBackend) AgentMapRender(context.Context, string) (string, error) { return "map", nil }
func (serveTestBackend) AgentMapStatus(context.Context) (MapStatus, error) {
	return MapStatus{Root: "/fixture"}, nil
}
func (serveTestBackend) AgentSymbolFind(context.Context, string) (any, error) {
	return []string{"match"}, nil
}
func (serveTestBackend) AgentFileExplain(context.Context, string) (any, error) {
	return map[string]string{"path": "a.go"}, nil
}
func (serveTestBackend) AgentFileContext(context.Context, string, string, string, int) (any, error) {
	return map[string]string{"query": "Main"}, nil
}

func TestRunServeAnswersEveryNDJSONRequest(t *testing.T) {
	t.Parallel()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"one","method":"pan/status"}`,
		`{"jsonrpc":"2.0","id":"two","method":"pan/status"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := RunServe(context.Background(), bufio.NewReader(strings.NewReader(input)), &output, serveTestBackend{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"id":"one"`) || !strings.Contains(lines[1], `"id":"two"`) {
		t.Fatalf("responses = %q", output.String())
	}
}

func TestRunServeMapsPanMethods(t *testing.T) {
	t.Parallel()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"map/render","params":{"format":"compact"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"map/status"}`,
		`{"jsonrpc":"2.0","id":3,"method":"symbol/find","params":{"query":"Main"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"file/explain","params":{"path":"a.go"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"file/context","params":{"query":"Main"}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := RunServe(context.Background(), bufio.NewReader(strings.NewReader(input)), &output, serveTestBackend{}); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(output.String()), "\n"); len(lines) != 5 {
		t.Fatalf("responses = %q", output.String())
	}
}

type changingServeBackend struct{ calls int }

func (b *changingServeBackend) AgentStatus(context.Context) (StatusSummary, error) {
	return StatusSummary{}, nil
}
func (b *changingServeBackend) AgentOverview(context.Context) (scan.OverviewReport, error) {
	return scan.OverviewReport{}, nil
}
func (b *changingServeBackend) AgentSymbols(context.Context, string, int) ([]analyze.Symbol, error) {
	return nil, nil
}
func (b *changingServeBackend) AgentReport(context.Context) (review.Document, error) {
	return fixedDocument(), nil
}
func (b *changingServeBackend) AgentMapRender(context.Context, string) (string, error) {
	return "", nil
}
func (b *changingServeBackend) AgentMapStatus(context.Context) (MapStatus, error) {
	b.calls++
	return MapStatus{BuiltAt: fmt.Sprintf("build-%d", b.calls)}, nil
}
func (b *changingServeBackend) AgentSymbolFind(context.Context, string) (any, error) { return nil, nil }
func (b *changingServeBackend) AgentFileExplain(context.Context, string) (any, error) {
	return nil, nil
}
func (b *changingServeBackend) AgentFileContext(context.Context, string, string, string, int) (any, error) {
	return nil, nil
}

func TestRunServeRefreshesBackendForEveryNDJSONRequest(t *testing.T) {
	t.Parallel()
	backend := &changingServeBackend{}
	input := `{"jsonrpc":"2.0","id":1,"method":"map/status"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"map/status"}` + "\n"
	var output bytes.Buffer
	if err := RunServe(context.Background(), bufio.NewReader(strings.NewReader(input)), &output, backend); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 2 || !strings.Contains(output.String(), "build-1") || !strings.Contains(output.String(), "build-2") {
		t.Fatalf("calls=%d responses=%q", backend.calls, output.String())
	}
}
