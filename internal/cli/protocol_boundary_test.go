package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/dotcommander/pan/internal/config"
)

func TestArtifactDashRejectedBeforeConfigEffects(t *testing.T) {
	t.Parallel()
	var out, diagnostics bytes.Buffer
	called := false
	err := Run(context.Background(), []string{"--artifact", "-", "scan", "overview"}, Deps{Out: &out, Err: &diagnostics, LoadConfig: func() (config.Config, error) { called = true; return config.Config{}, nil }})
	if err == nil || called || out.Len() != 0 {
		t.Fatalf("err=%v config=%v stdout=%q", err, called, out.String())
	}
}

func TestParserDiagnosticsUseStderr(t *testing.T) {
	t.Parallel()
	var out, diagnostics bytes.Buffer
	err := Run(context.Background(), []string{"--unknown-fixture-option"}, Deps{Out: &out, Err: &diagnostics})
	if err == nil || out.Len() != 0 {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, out.String(), diagnostics.String())
	}
}
