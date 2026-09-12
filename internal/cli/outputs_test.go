package cli_test

import (
	"testing"

	"github.com/dotcommander/pan/internal/cli"
)

func TestBuildOutputCatalogIncludesSourceEvalAlias(t *testing.T) {
	t.Parallel()
	for _, surface := range cli.BuildOutputCatalog().Surfaces {
		if surface.Name != "eval" {
			continue
		}
		if surface.Producer != "review eval --json" || surface.Schema != "pan.eval/v1" {
			t.Fatalf("eval alias = %#v", surface)
		}
		return
	}
	t.Fatal("missing eval source alias")
}
