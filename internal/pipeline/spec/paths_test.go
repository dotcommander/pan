package spec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func TestResolve_BareName(t *testing.T) {
	t.Parallel()
	dataDir, err := spec.DataDir()
	require.NoError(t, err)
	got, err := spec.Resolve("paper")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dataDir, "paper.yaml"), got)
}

func TestResolve_Extension(t *testing.T) {
	t.Parallel()
	got, err := spec.Resolve("paper.yaml")
	require.NoError(t, err)
	require.Equal(t, "paper.yaml", got)
}

func TestResolve_Separator(t *testing.T) {
	t.Parallel()
	got, err := spec.Resolve("examples/paper.yaml")
	require.NoError(t, err)
	require.Equal(t, "examples/paper.yaml", got)
}

func TestResolve_CWDFallthrough(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// create a file named "paper" (no extension) in the temp dir
	require.NoError(t, os.WriteFile(filepath.Join(dir, "paper"), []byte("x"), 0o600))
	got, err := spec.Resolve("paper")
	require.NoError(t, err)
	require.Equal(t, "paper", got)
}

func TestResolve_Empty(t *testing.T) {
	t.Parallel()
	_, err := spec.Resolve("")
	require.Error(t, err)
}

func TestUserDir_Contains_AppName(t *testing.T) {
	t.Parallel()
	dir, err := spec.UserDir()
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(dir, "/pan"), "expected dir to end with /pan, got %s", dir)
}

func TestDataDir_UnderUserDir(t *testing.T) {
	t.Parallel()
	userDir, err := spec.UserDir()
	require.NoError(t, err)
	dataDir, err := spec.DataDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(userDir, "pipelines"), dataDir)
	require.True(t, strings.HasSuffix(dataDir, "/pan/pipelines"),
		"expected dir to end with /pan/pipelines, got %s", dataDir)
}

func TestProjectName_GoMod(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/dotcommander/awesome-tool\n\ngo 1.26\n"), 0o644))

	got := spec.ProjectName(dir)
	require.Equal(t, "awesome-tool", got)
}

func TestProjectName_BareDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got := spec.ProjectName(dir)
	require.Equal(t, filepath.Base(dir), got)
}

func TestProjectName_PackageJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name": "my-pkg", "version": "1.0.0"}`), 0o644))

	got := spec.ProjectName(dir)
	require.Equal(t, "my-pkg", got)
}

func TestProjectName_GoModTakesPrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/dotcommander/from-go\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name": "from-pkg"}`), 0o644))

	got := spec.ProjectName(dir)
	require.Equal(t, "from-go", got)
}

func TestProjectName_Fallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Use a nested subdirectory with a known name
	subdir := filepath.Join(dir, "myproject")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	got := spec.ProjectName(subdir)
	require.Equal(t, "myproject", got)
}

func TestScanOutputPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/dotcommander/testapp\n"), 0o644))

	dataDir, err := spec.DataDir()
	require.NoError(t, err)

	path, err := spec.ScanOutputPath(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dataDir, "testapp.yaml"), path)
	// Verify the data dir was created
	info, err := os.Stat(dataDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}
