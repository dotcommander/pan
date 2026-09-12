package scan

import (
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFillEmptyPhases_FillsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	rel := writeFile(t, dir, "engine.go", `package engine

// readFile reads a file from disk and returns its contents.
func readFile(path string) ([]byte, error) {
	_ = path
	_ = 1
	_ = 2
	_ = 3
	return nil, nil
}

// checkPath validates the given path against security rules.
func checkPath(p string) bool {
	_ = p
	_ = 1
	_ = 2
	_ = 3
	return true
}
`)

	phases := []spec.Phase{{Name: "Engine", Files: []string{rel}, Stages: nil}}
	got := fillEmptyPhases(dir, phases, 10)

	require.Len(t, got, 1)
	require.GreaterOrEqual(t, len(got[0].Stages), 2)

	var labels []string
	for _, s := range got[0].Stages {
		if s.Chip != nil {
			labels = append(labels, s.Chip.Label)
		}
	}
	assert.Contains(t, labels, "checkPath")
	assert.Contains(t, labels, "readFile")
}

func TestFillEmptyPhases_SkipsNonEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	writeFile(t, dir, "engine.go", `package engine

func bigFunc() {
	_ = 1; _ = 2; _ = 3; _ = 4; _ = 5
}
`)

	existing := spec.Stage{Chip: &spec.Chip{Label: "Existing"}}
	phases := []spec.Phase{{Name: "Engine", Files: []string{"engine.go"}, Stages: []spec.Stage{existing}}}
	got := fillEmptyPhases(dir, phases, 10)

	require.Len(t, got[0].Stages, 1)
	assert.Equal(t, "Existing", got[0].Stages[0].Chip.Label)
}

func TestFillEmptyPhases_FiltersTrivial(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	rel := writeFile(t, dir, "tiny.go", `package tiny

func getter() int {
	return 42
}

func bigEnough() int {
	x := 1
	y := 2
	z := x + y
	w := z * 2
	return w
}
`)

	phases := []spec.Phase{{Name: "Tiny", Files: []string{rel}, Stages: nil}}
	got := fillEmptyPhases(dir, phases, 10)

	require.Len(t, got, 1)
	var labels []string
	for _, s := range got[0].Stages {
		if s.Chip != nil {
			labels = append(labels, s.Chip.Label)
		}
	}
	assert.Contains(t, labels, "bigEnough")
	assert.NotContains(t, labels, "getter")
}

func TestFillEmptyPhases_ExportedFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	rel := writeFile(t, dir, "mixed.go", `package mixed

func zebra() int {
	x := 1
	y := 2
	z := x + y
	w := z * 2
	return w
}

func Alpha() int {
	x := 1
	y := 2
	z := x + y
	w := z * 2
	return w
}

func beta() int {
	x := 1
	y := 2
	z := x + y
	w := z * 2
	return w
}
`)

	phases := []spec.Phase{{Name: "Mixed", Files: []string{rel}, Stages: nil}}
	got := fillEmptyPhases(dir, phases, 10)

	require.Len(t, got, 1)
	require.GreaterOrEqual(t, len(got[0].Stages), 3)
	assert.Equal(t, "Alpha", got[0].Stages[0].Chip.Label)
	assert.Equal(t, "beta", got[0].Stages[1].Chip.Label)
	assert.Equal(t, "zebra", got[0].Stages[2].Chip.Label)
}
