package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestScanDiffRejectsNegativeTop(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	deps := newTestDeps(&out)
	err := cli.Run(context.Background(), []string{"--repo", basicGoRepo(), "scan", "diff", "--top", "-1"}, deps)
	if err == nil || !strings.Contains(err.Error(), "--top") {
		t.Fatalf("err = %v, want --top rejection", err)
	}
}

func TestScanDiffHelpDocumentsRangeGrammar(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	deps := newTestDeps(&out)
	if err := cli.Run(context.Background(), []string{"scan", "diff", "--help"}, deps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"A..B", "working tree", "R..HEAD"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, out.String())
		}
	}
}
