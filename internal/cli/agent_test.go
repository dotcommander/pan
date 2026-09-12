package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/config"
)

func newAgentDeps(input string) cli.Deps {
	cfg := config.Config{MaxFiles: 100, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxNodes: 20, OutputBudget: 1024, CommandTimeout: time.Second}
	return cli.Deps{App: app.New(app.Deps{Config: cfg}), In: strings.NewReader(input)}
}

// TestAgentStdioHelloAndReportOverStdin exercises the full stdin wiring:
// requests flow through Deps.In exactly as main wires os.Stdin, and every
// request receives exactly one NDJSON response.
func TestAgentStdioHelloAndReportOverStdin(t *testing.T) {
	t.Parallel()
	input := `{"schema":"pan.agent/v1","id":"h1","op":"hello"}` + "\n" +
		`{"schema":"pan.agent/v1","id":"r1","op":"report"}` + "\n"
	var out bytes.Buffer
	deps := newAgentDeps(input)
	deps.Out = &out
	if err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "agent", "stdio"}, deps); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 responses, got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"id":"h1"`) || !strings.Contains(lines[0], `"ok":true`) ||
		!strings.Contains(lines[0], `"report_schema":"pan.review-report/v1"`) {
		t.Fatalf("hello response: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"id":"r1"`) || !strings.Contains(lines[1], `"schema":"pan.review-report/v1"`) ||
		!strings.Contains(lines[1], `"report_id"`) {
		t.Fatalf("report response: %s", lines[1])
	}
}

// TestAgentStdioIsDeterministic proves the parity guarantee: identical
// request sessions over the same repository yield byte-identical output.
func TestAgentStdioIsDeterministic(t *testing.T) {
	t.Parallel()
	input := `{"schema":"pan.agent/v1","id":"r","op":"report"}` + "\n"
	var first, second bytes.Buffer
	depsOne := newAgentDeps(input)
	depsOne.Out = &first
	depsTwo := newAgentDeps(input)
	depsTwo.Out = &second
	args := []string{"--repo", basicGoRepo(), "agent", "stdio"}
	if err := cli.Run(context.Background(), args, depsOne); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(context.Background(), args, depsTwo); err != nil {
		t.Fatal(err)
	}
	if first.String() == "" {
		t.Fatal("agent stdio produced no output")
	}
	if first.String() != second.String() {
		t.Fatalf("agent stdio is not deterministic:\n%s\n---\n%s", first.String(), second.String())
	}
}

func TestScanDoctorEmitsHealthReport(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "--format", "json", "scan", "doctor"}, newTestDeps(&out))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"schema_version": "pan/v1"`,
		`"schema": "pan.doctor/v1"`,
		`"config"`,
		`"analysis"`,
		`"git"`,
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestReviewOutputsCatalog(t *testing.T) {
	t.Parallel()
	t.Run("full inventory lists implemented surfaces", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		if err := cli.Run(context.Background(), []string{"--format", "json", "review", "outputs"}, newTestDeps(&out)); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`"schema": "pan.output-catalog/v1"`,
			`"name": "agent-stdio"`,
			`"name": "doctor"`,
			`"name": "review-report-json"`,
		} {
			if !bytes.Contains(out.Bytes(), []byte(want)) {
				t.Fatalf("output missing %q:\n%s", want, out.String())
			}
		}
	})
	t.Run("surface filter narrows to one entry", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		if err := cli.Run(context.Background(), []string{"--format", "json", "review", "outputs", "doctor"}, newTestDeps(&out)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(out.Bytes(), []byte(`"name": "doctor"`)) || bytes.Contains(out.Bytes(), []byte(`"name": "agent-stdio"`)) {
			t.Fatalf("filtered catalog wrong:\n%s", out.String())
		}
	})
	t.Run("unknown surface fails", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		err := cli.Run(context.Background(), []string{"review", "outputs", "nope"}, newTestDeps(&out))
		if err == nil || !strings.Contains(err.Error(), `unknown output surface "nope"`) {
			t.Fatalf("err = %v, want unknown surface failure", err)
		}
		if code := cli.ExitCode(err); code != cli.ExitFailure {
			t.Fatalf("exit code = %d, want %d", code, cli.ExitFailure)
		}
	})
}
