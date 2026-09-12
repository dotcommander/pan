package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes content to a file in dir, creating the file.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return name // return relative name for use in phase.Files
}

// gomod writes a minimal go.mod into dir.
func gomod(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.22\n")
}

// phase1 builds a single-phase spec with the given file and chip labels.
func phase1(files []string, labels ...string) []spec.Phase {
	stages := make([]spec.Stage, 0, len(labels))
	for _, l := range labels {
		stages = append(stages, spec.Stage{Chip: &spec.Chip{Label: l}})
	}
	return []spec.Phase{{Name: "Test", Files: files, Stages: stages}}
}

// ─── detectForks ─────────────────────────────────────────────────────────────

func TestDetectForks_SwitchStatement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "main.go", `package main

func Run(x string) {
	switch x {
	case "a":
		handleA()
	case "b":
		handleB()
	case "c":
		handleC()
	}
}

func handleA() {}
func handleB() {}
func handleC() {}
`)
	phases := phase1([]string{rel}, "Run")
	got := detectForks(dir, phases)

	require.Len(t, got, 1)
	require.Len(t, got[0].Stages, 1)
	s := got[0].Stages[0]
	require.NotNil(t, s.Fork, "expected Fork stage")
	assert.Equal(t, "x", s.Fork.Gate)
	assert.Len(t, s.Fork.Branches, 3)
	assert.Equal(t, `"a"`, s.Fork.Branches[0].Condition)
	assert.Equal(t, `"b"`, s.Fork.Branches[1].Condition)
	assert.Equal(t, `"c"`, s.Fork.Branches[2].Condition)
}

func TestDetectForks_IfElseChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "main.go", `package main

func Dispatch(kind string) {
	if kind == "html" {
		renderHTML()
	} else if kind == "json" {
		renderJSON()
	} else {
		renderOther()
	}
}

func renderHTML()  {}
func renderJSON()  {}
func renderOther() {}
`)
	phases := phase1([]string{rel}, "Dispatch")
	got := detectForks(dir, phases)

	require.Len(t, got, 1)
	s := got[0].Stages[0]
	require.NotNil(t, s.Fork, "expected Fork stage")
	assert.Len(t, s.Fork.Branches, 3)
	assert.Equal(t, `kind == "html"`, s.Fork.Branches[0].Condition)
	assert.Equal(t, `kind == "json"`, s.Fork.Branches[1].Condition)
	assert.Equal(t, "else", s.Fork.Branches[2].Condition)
}

func TestDetectForks_SelectStatement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "main.go", `package main

func Listen(done chan struct{}, msg chan string, err chan error) {
	select {
	case <-done:
		handleDone()
	case m := <-msg:
		handleMsg(m)
	case e := <-err:
		handleErr(e)
	}
}

func handleDone()        {}
func handleMsg(m string) {}
func handleErr(e error)  {}
`)
	phases := phase1([]string{rel}, "Listen")
	got := detectForks(dir, phases)

	require.Len(t, got, 1)
	s := got[0].Stages[0]
	require.NotNil(t, s.Fork, "expected Fork stage")
	assert.Equal(t, "select", s.Fork.Gate)
	assert.Len(t, s.Fork.Branches, 3)
}

func TestDetectForks_SkipsConversionOnlyTypeSwitch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "main.go", `package main

func MetadataFloat(raw any) float64 {
	switch value := raw.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}
`)
	phases := phase1([]string{rel}, "MetadataFloat")
	got := detectForks(dir, phases)

	require.NotNil(t, got[0].Stages[0].Chip)
	require.Nil(t, got[0].Stages[0].Fork)
}

func TestDetectForks_SkipsNonMatchingFunc(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	// The file has a switch in helper(), but the chip label is "Run".
	// Run() has no fork-like construct — it should stay a chip.
	rel := writeFile(t, dir, "main.go", `package main

func Run() {
	helper("x")
}

func helper(x string) {
	switch x {
	case "a":
		println("a")
	case "b":
		println("b")
	}
}
`)
	phases := phase1([]string{rel}, "Run")
	got := detectForks(dir, phases)

	require.Len(t, got, 1)
	s := got[0].Stages[0]
	assert.NotNil(t, s.Chip, "Run() should remain a chip — fork is in helper()")
	assert.Nil(t, s.Fork)
}

// ─── detectFanouts ───────────────────────────────────────────────────────────

func TestDetectFanouts_AddCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "root.go", `package main

import "github.com/spf13/cobra"

func Execute() {
	root := &cobra.Command{}
	root.AddCommand(newInitCmd())
	root.AddCommand(newRenderCmd())
	root.AddCommand(newServeCmd())
}
`)
	phases := phase1([]string{rel}, "Execute")
	got := detectFanouts(dir, phases)

	require.Len(t, got, 1)
	require.Len(t, got[0].Stages, 2, "should have chip + fanout")
	chip := got[0].Stages[0]
	assert.NotNil(t, chip.Chip)
	fanout := got[0].Stages[1]
	require.NotNil(t, fanout.Fanout)
	assert.Equal(t, gateSubcommand, fanout.Fanout.Gate)
	require.Len(t, fanout.Fanout.Targets, 3)
	assert.Equal(t, "newInitCmd", fanout.Fanout.Targets[0].Label)
	assert.Equal(t, "init", fanout.Fanout.Targets[0].Flag)
	assert.Equal(t, "newRenderCmd", fanout.Fanout.Targets[1].Label)
	assert.Equal(t, "render", fanout.Fanout.Targets[1].Flag)
	assert.Equal(t, "newServeCmd", fanout.Fanout.Targets[2].Label)
	assert.Equal(t, "serve", fanout.Fanout.Targets[2].Flag)
}

func TestDetectFanouts_MultipleGoroutines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "server.go", `package main

func Start() {
	go watchLoop()
	go handleShutdown()
}

func watchLoop() {}
func handleShutdown() {}
`)
	phases := phase1([]string{rel}, "Start")
	got := detectFanouts(dir, phases)

	require.Len(t, got, 1)
	require.Len(t, got[0].Stages, 2, "should have chip + fanout")
	fanout := got[0].Stages[1]
	require.NotNil(t, fanout.Fanout)
	assert.Equal(t, "goroutines", fanout.Fanout.Gate)
	require.Len(t, fanout.Fanout.Targets, 2)
	assert.Equal(t, "watchLoop", fanout.Fanout.Targets[0].Label)
	assert.Equal(t, "handleShutdown", fanout.Fanout.Targets[1].Label)
}

// ─── detectStores ─────────────────────────────────────────────────────────────

func TestDetectStores_EmbedIsSourceNotStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "templates.go", `package main

import "embed"

//go:embed templates/*.html
var TemplateFS embed.FS
`)
	phases := phase1([]string{rel})
	got, _ := detectStores(dir, phases)

	require.Empty(t, got)
}

func TestDetectStores_EmbedAddsFilesToPhase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	// Create template files that match the embed pattern.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	writeFile(t, dir, "templates/layout.html", "<html>{{.}}</html>")
	writeFile(t, dir, "templates/index.html", "<html>index</html>")

	rel := writeFile(t, dir, "templates.go", `package main

import "embed"

//go:embed templates/*.html
var TemplateFS embed.FS
`)
	phases := phase1([]string{rel})
	_, gotPhases := detectStores(dir, phases)

	require.Len(t, gotPhases, 1)
	// The phase should now contain the original .go file plus the two .html files.
	assert.Contains(t, gotPhases[0].Files, rel, "original .go file should be present")
	assert.Contains(t, gotPhases[0].Files, "templates/layout.html")
	assert.Contains(t, gotPhases[0].Files, "templates/index.html")
}

func TestDetectStores_EmbedSkipsGoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "static"), 0o755))
	writeFile(t, dir, "static/app.js", "console.log('hi')")
	writeFile(t, dir, "static/helper.go", "package static\n\nconst X = 1\n")

	rel := writeFile(t, dir, "embed.go", `package main

import "embed"

//go:embed static/*
var StaticFS embed.FS
`)
	phases := phase1([]string{rel})
	_, gotPhases := detectStores(dir, phases)

	require.Len(t, gotPhases, 1)
	assert.Contains(t, gotPhases[0].Files, "static/app.js", ".js file should be added")
	assert.NotContains(t, gotPhases[0].Files, "static/helper.go", ".go file should be skipped")
}

func TestDetectStores_ReadWriteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "io.go", `package main

import "os"

func Load() {
	_, _ = os.ReadFile("config.yaml")
}

func Save() {
	_ = os.WriteFile("out/result.html", nil, 0644)
}
`)
	phases := phase1([]string{rel}, "Load", "Save")
	got, _ := detectStores(dir, phases)

	require.Len(t, got, 2)

	// Stores are sorted alphabetically by name.
	names := []string{got[0].Name, got[1].Name}
	assert.Contains(t, names, "config.yaml")
	assert.Contains(t, names, "out/result.html")

	for _, s := range got {
		require.Len(t, s.Writers, 1)
		switch s.Name {
		case "config.yaml":
			assert.Equal(t, "r", s.Writers[0].Access)
			assert.Equal(t, "Load", s.Writers[0].Stage)
		case "out/result.html":
			assert.Equal(t, "w", s.Writers[0].Access)
			assert.Equal(t, "Save", s.Writers[0].Stage)
		}
	}
}

func TestDetectStores_PersistenceImport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "repo.go", `package main

import (
	_ "github.com/jackc/pgx/v5"
)

func Query() {}
`)
	phases := phase1([]string{rel})
	got, _ := detectStores(dir, phases)

	require.Len(t, got, 1, "a pgx import should yield exactly one datastore")
	assert.Equal(t, "PostgreSQL", got[0].Name)
	require.Len(t, got[0].Writers, 1)
	assert.Equal(t, "Test", got[0].Writers[0].Stage)
	assert.Equal(t, "r/w", got[0].Writers[0].Access)
	assert.Equal(t, "persistence", got[0].Writers[0].Note)
}

func TestDetectStores_Dedup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "store.go", `package main

import "os"

func Load() {
	_, _ = os.ReadFile("data.json")
}

func Save() {
	_ = os.WriteFile("data.json", nil, 0644)
}
`)
	phases := phase1([]string{rel}, "Load", "Save")
	got, _ := detectStores(dir, phases)

	// Same path read+write → 1 store, 2 writers.
	require.Len(t, got, 1, "dedup should produce a single store")
	assert.Equal(t, "data.json", got[0].Name)
	assert.Len(t, got[0].Writers, 2)

	accesses := map[string]bool{}
	for _, w := range got[0].Writers {
		accesses[w.Access] = true
	}
	assert.True(t, accesses["r"], "should have read writer")
	assert.True(t, accesses["w"], "should have write writer")
}

func TestDetectStoresFallsBackToExistingPhaseLabel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "config.go", `package main

import "os"

func Save() {
	_ = os.WriteFile("config.json", nil, 0600)
}
`)
	phases := []spec.Phase{{Name: "Config", Files: []string{rel}}}
	got, _ := detectStores(dir, phases)

	require.Len(t, got, 1)
	require.Len(t, got[0].Writers, 1)
	assert.Equal(t, "Config", got[0].Writers[0].Stage)
}

func TestDetectStoresDynamicFinalPathsAndAtomicRename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)
	rel := writeFile(t, dir, "cache.go", `package main

import "os"

type service struct { cachePath string }

func getConfigPath() string { return "config.json" }

func SaveConfig() {
	path := getConfigPath()
	_ = os.WriteFile(path, nil, 0600)
}

func (s *service) SaveCache(tmpPath string) {
	_ = os.Rename(tmpPath, s.cachePath)
}
`)
	phases := phase1([]string{rel}, "SaveConfig", "SaveCache")
	got, _ := detectStores(dir, phases)

	byName := make(map[string]spec.Store, len(got))
	for _, store := range got {
		byName[store.Name] = store
	}
	require.Contains(t, byName, "config.json")
	require.Contains(t, byName, "cache file")
	require.Equal(t, "atomic replace", byName["cache file"].Writers[0].Note)
	require.Equal(t, rel, byName["cache file"].Writers[0].SourceFile)
	require.Positive(t, byName["cache file"].Writers[0].SourceLine)
	require.Equal(t, []string{"cachePath"}, byName["cache file"].Writers[0].SourceSymbols)
}

// TestDetectFanouts_RoutingClosure verifies that a single mux.HandleFunc
// wrapping an internal dispatch closure is detected as an HTTP routes fanout.
// The closure uses switch len(parts) — only non-NotFound arms are targets.
func TestDetectFanouts_RoutingClosure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	rel := writeFile(t, dir, "handler.go", `package main

import "net/http"

func makeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch len(r.URL.Path) {
		case 0:
			serveIndex(w, r)
		case 1:
			serveCollection(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

func serveIndex(w http.ResponseWriter, r *http.Request)      {}
func serveCollection(w http.ResponseWriter, r *http.Request) {}
`)

	// No chip labels — tests second pass (fallback that scans any function).
	phases := phase1([]string{rel})
	got := detectFanouts(dir, phases)

	require.Len(t, got, 1)
	// Find the fanout stage.
	var fanout *spec.Fanout
	for _, s := range got[0].Stages {
		if s.Fanout != nil {
			fanout = s.Fanout
			break
		}
	}
	require.NotNil(t, fanout, "expected HTTP routes fanout stage")
	assert.Equal(t, "HTTP routes", fanout.Gate)
	// NotFound arm should be filtered; serveIndex and serveCollection should remain.
	require.Len(t, fanout.Targets, 2, "NotFound should be filtered out")
	labels := []string{fanout.Targets[0].Label, fanout.Targets[1].Label}
	assert.Contains(t, labels, "serveIndex")
	assert.Contains(t, labels, "serveCollection")
}

func TestDetectForks_BranchWideAcrossSubPhases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gomod(t, dir)

	rel1 := writeFile(t, dir, "dispatch.go", `package main

func Dispatch(kind string) {
	switch kind {
	case "big":
		handleBig()
	case "small":
		handleSmall()
	}
}
`)
	rel2 := writeFile(t, dir, "handlers.go", `package main

func handleBig() {
`+strings.Repeat("\t_ = 1\n", 35)+`}

func handleSmall() {
	_ = 1
}
`)

	phases := []spec.Phase{
		{Name: "Dispatch", Files: []string{rel1}, Stages: []spec.Stage{
			{Chip: &spec.Chip{Label: "Dispatch"}},
		}},
		{Name: "Handlers", Files: []string{rel2}, Stages: []spec.Stage{
			{Chip: &spec.Chip{Label: "handleBig"}},
			{Chip: &spec.Chip{Label: "handleSmall"}},
		}},
	}

	got := detectForks(dir, phases)

	require.NotNil(t, got[0].Stages[0].Fork, "Dispatch should become a fork")
	fork := got[0].Stages[0].Fork
	require.Len(t, fork.Branches, 2)
	assert.True(t, fork.Branches[0].Wide, "handleBig branch should be wide (>30 lines)")
	assert.False(t, fork.Branches[1].Wide, "handleSmall branch should not be wide")
}
