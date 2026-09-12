package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
	"github.com/stretchr/testify/require"
)

func TestComputeCoverageCountsUtilityNamedProductionFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "helpers.go")
	require.NoError(t, os.WriteFile(path, []byte("package example\n"), 0o600))
	ranked := []symbols.RankedFile{{FileSymbols: &symbols.FileSymbols{
		Path:     "helpers.go",
		Language: literalGo,
	}}}
	phases := []spec.Phase{{Name: "Helpers", Files: []string{"helpers.go"}}}

	coverage := computeCoverage(ranked, phases, root, scanMaxFileSize)
	require.Equal(t, 1, coverage.Total)
	require.Equal(t, 1, coverage.Represented)
}
