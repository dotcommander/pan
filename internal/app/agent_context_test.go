package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/agent"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/review"
)

func TestAgentReportRefreshesAfterMutationBetweenStdioRequests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "fixture.go")
	original := []byte("package fixture\nfunc Main() {}\n")
	changed := []byte("package fixture\nfunc Mane() {}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(Deps{Config: config.Config{MaxFiles: 20, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, MaxInstructions: 5, OutputBudget: 1024, CommandTimeout: time.Second}})
	state := service.AgentServe(root)
	builds := 0
	build := func(ctx context.Context) (review.Document, error) {
		builds++
		doc, err := state.AgentReport(ctx)
		if err != nil || builds != 1 {
			return doc, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return review.Document{}, err
		}
		if err := os.WriteFile(path, changed, 0o600); err != nil {
			return review.Document{}, err
		}
		return doc, os.Chtimes(path, info.ModTime(), info.ModTime())
	}
	input := `{"schema":"pan.agent/v1","id":"one","op":"report"}` + "\n" + `{"schema":"pan.agent/v1","id":"two","op":"report"}` + "\n"
	var output bytes.Buffer
	if err := agent.Run(context.Background(), bufio.NewReader(strings.NewReader(input)), &output, build); err != nil {
		t.Fatal(err)
	}
	if builds != 2 || strings.Count(output.String(), `"ok":true`) != 2 {
		t.Fatalf("builds=%d responses=%q", builds, output.String())
	}
	row := review.ReadItem{EvidenceID: "fixture", Path: "fixture.go"}
	packet, err := state.AgentContext(context.Background(), review.Document{ReportID: "report"}, []review.ReadItem{row}, 4096)
	if err != nil || len(packet.Targets) != 1 || packet.Targets[0].Content != string(changed) {
		t.Fatalf("context after stdio refresh=%#v err=%v", packet, err)
	}
}

func TestAgentContextUsesCapturedSourceAndRefusesStaleTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "fixture.go")
	if err := os.WriteFile(path, []byte("package fixture\nfunc Main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("API_KEY=must-not-leak\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(Deps{Config: config.Config{MaxFiles: 20, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, MaxInstructions: 5, OutputBudget: 1024, CommandTimeout: time.Second}})
	state := service.AgentServe(root)
	if _, err := state.AgentStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	row := review.ReadItem{EvidenceID: "fixture", Path: "fixture.go", Why: []string{"fixture"}}
	doc := review.Document{Schema: review.DocumentSchema, ReportID: "report", ReadQueue: []review.ReadItem{row}}
	packet, err := state.AgentContext(context.Background(), doc, []review.ReadItem{row}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Targets) != 1 || packet.Targets[0].Content != "package fixture\nfunc Main() {}\n" || packet.Fingerprint == "" {
		t.Fatalf("packet = %#v", packet)
	}
	assertAgentContextBounds(t, state, doc, row)
	assertAgentContextRefresh(t, state, doc, row, path)
}

func assertAgentContextRefresh(t *testing.T, state *AgentServeState, doc review.Document, row review.ReadItem, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte("package fixture\nfunc Mane() {}\n")
	if writeErr := os.WriteFile(path, changed, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if timeErr := os.Chtimes(path, info.ModTime(), info.ModTime()); timeErr != nil {
		t.Fatal(timeErr)
	}
	if verifyErr := state.AgentVerify(context.Background(), doc); !errors.Is(verifyErr, agent.ErrStaleEvidence) {
		t.Fatalf("verify error = %v, want stale evidence", verifyErr)
	}
	if _, statusErr := state.AgentStatus(context.Background()); statusErr != nil {
		t.Fatalf("rebuild after changed source: %v", statusErr)
	}
	updated, err := state.AgentContext(context.Background(), doc, []review.ReadItem{row}, 4096)
	if err != nil || len(updated.Targets) != 1 || updated.Targets[0].Content != string(changed) {
		t.Fatalf("rebuilt context=%#v err=%v", updated, err)
	}
}

func assertAgentContextBounds(t *testing.T, state *AgentServeState, doc review.Document, row review.ReadItem) {
	t.Helper()
	secret := review.ReadItem{EvidenceID: "secret", Path: ".env"}
	redacted, err := state.AgentContext(context.Background(), doc, []review.ReadItem{secret}, 4096)
	if err != nil || len(redacted.Targets) != 0 || len(redacted.Omissions) != 1 || strings.Contains(strings.Join(redacted.Omissions, ""), "must-not-leak") {
		t.Fatalf("redacted packet=%#v err=%v", redacted, err)
	}
	if _, budgetErr := state.AgentContext(context.Background(), doc, []review.ReadItem{row}, 1); !errors.Is(budgetErr, agent.ErrContextBudgetTooSmall) {
		t.Fatalf("small budget error = %v, want context budget too small", budgetErr)
	}
}

func TestAgentContextOmitsInlineSourceCredentials(t *testing.T) {
	t.Parallel()
	cases := []string{
		`package fixture; const value = "sk-` + strings.Repeat("a", 24) + `"`,
		`package fixture; func main() { password := "synthetic-value"; _ = password }`,
		`{"api_key": "synthetic-value"}`,
		`api_key: "synthetic-value"`,
		`Authorization: Bearer synthetic-value`,
		`-----BEGIN PRIVATE KEY-----`,
	}
	for _, content := range cases {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		service := New(Deps{Config: config.Config{MaxFiles: 20, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, MaxInstructions: 5, OutputBudget: 1024, CommandTimeout: time.Second}})
		state := service.AgentServe(root)
		if _, err := state.AgentStatus(context.Background()); err != nil {
			t.Fatal(err)
		}
		packet, err := state.AgentContext(context.Background(), review.Document{ReportID: "report"}, []review.ReadItem{{Path: "main.go"}}, 4096)
		if err != nil || len(packet.Targets) != 0 || len(packet.Omissions) != 1 || packet.Omissions[0] != "main.go: redacted secret source" {
			t.Fatalf("inline credential was not omitted: err=%v", err)
		}
		if _, retained := state.sources["main.go"]; retained {
			t.Fatal("credential source retained in session capture")
		}
	}
}
