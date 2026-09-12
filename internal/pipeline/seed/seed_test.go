package seed_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/pan/internal/pipeline/seed"
)

func TestSeed_WritesAllFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	written, err := seed.Seed(dir, false)
	require.NoError(t, err)
	require.Len(t, written, len(seed.Files()))
	for _, p := range written {
		require.FileExists(t, p)
		got, rerr := os.ReadFile(p)
		require.NoError(t, rerr)
		require.NotEmpty(t, got)
	}
}

func TestSeed_RefusesOverwriteWithoutForce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// pre-create paper.yaml
	require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.yaml"), []byte("existing"), 0o600))
	_, err := seed.Seed(dir, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "paper.yaml")
}

func TestSeed_ForceOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.yaml"), []byte("old"), 0o600))
	written, err := seed.Seed(dir, true)
	require.NoError(t, err)
	require.Len(t, written, len(seed.Files()))
}

func TestIsEmptyDir_Missing(t *testing.T) {
	t.Parallel()
	empty, err := seed.IsEmptyDir("/nonexistent/path/that/does/not/exist")
	require.NoError(t, err)
	require.True(t, empty)
}

func TestIsEmptyDir_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	empty, err := seed.IsEmptyDir(dir)
	require.NoError(t, err)
	require.True(t, empty)
}

func TestIsEmptyDir_HasYaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.yaml"), []byte("x"), 0o600))
	empty, err := seed.IsEmptyDir(dir)
	require.NoError(t, err)
	require.False(t, empty)
}

func TestIsEmptyDir_OnlyNonYaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
	empty, err := seed.IsEmptyDir(dir)
	require.NoError(t, err)
	require.True(t, empty)
}

func TestFiles_Deterministic(t *testing.T) {
	t.Parallel()
	first := seed.Files()
	second := seed.Files()
	require.Equal(t, first, second)
	require.NotEmpty(t, first)
	// verify sorted
	for i := 1; i < len(first); i++ {
		require.LessOrEqual(t, first[i-1], first[i], "Files() should be sorted")
	}
}

func TestSeedRejectsSymlinkBeforeWriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o600))
	names := seed.Files()
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, names[len(names)-1])))
	_, err := seed.Seed(dir, true)
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(dir, names[0]))
	content, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "keep", string(content))
}
