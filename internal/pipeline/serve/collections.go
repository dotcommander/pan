package serve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dotcommander/pan/internal/pipeline/render"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

var errSpecCollision = errors.New("spec collision")

type collectionPlan struct {
	name, sourcePath string
	specs            []specFile
}

type specFile struct{ basename, path string }

type specRef struct{ collection, name, sourcePath, collectionSource string }

type loadedSpec struct {
	path string
	spec *spec.Spec
	html []byte
}

type loadedCollection struct {
	sourcePath string
	specs      map[string]*loadedSpec
}

type collectionSet struct {
	mu          sync.RWMutex
	collections map[string]*loadedCollection
}

func newCollectionSet() *collectionSet {
	return &collectionSet{collections: make(map[string]*loadedCollection)}
}

func (cs *collectionSet) add(ref specRef) error {
	s, err := spec.Load(ref.sourcePath)
	if err != nil {
		return fmt.Errorf("load %s: %w", ref.sourcePath, err)
	}
	html, err := render.ToBytes(s, spec.ProjectRootForSpec(s, ref.sourcePath, ""))
	if err != nil {
		return fmt.Errorf("render %s: %w", ref.sourcePath, err)
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	collection := cs.collections[ref.collection]
	if collection == nil {
		collection = &loadedCollection{sourcePath: ref.collectionSource, specs: make(map[string]*loadedSpec)}
		cs.collections[ref.collection] = collection
	}
	collection.specs[ref.name] = &loadedSpec{path: ref.sourcePath, spec: s, html: html}
	return nil
}

func (cs *collectionSet) remove(ref specRef) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	collection := cs.collections[ref.collection]
	if collection == nil {
		return
	}
	delete(collection.specs, ref.name)
	if len(collection.specs) == 0 {
		delete(cs.collections, ref.collection)
	}
}

func (cs *collectionSet) summaries() []spec.CollectionSummary {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	collections := make([]spec.CollectionSummary, 0, len(cs.collections))
	for name, collection := range cs.collections {
		summaries := make([]spec.Summary, 0, len(collection.specs))
		for basename, loaded := range collection.specs {
			summaries = append(summaries, loaded.spec.AsSummary(loaded.path, name, basename))
		}
		sort.Slice(summaries, func(i, j int) bool { return summaries[i].Basename < summaries[j].Basename })
		collections = append(collections, spec.CollectionSummary{Name: name, SourcePath: collection.sourcePath, Specs: summaries})
	}
	sort.Slice(collections, func(i, j int) bool { return collections[i].Name < collections[j].Name })
	return collections
}

func (cs *collectionSet) collection(name string) (spec.CollectionSummary, bool) {
	for _, collection := range cs.summaries() {
		if collection.Name == name {
			return collection, true
		}
	}
	return spec.CollectionSummary{}, false
}

func (cs *collectionSet) page(collection, name string) ([]byte, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	c := cs.collections[collection]
	if c == nil || c.specs[name] == nil {
		return nil, false
	}
	return c.specs[name].html, true
}
func expandPaths(paths []string) ([]collectionPlan, error) {
	plans := make([]collectionPlan, 0, len(paths))
	seen := map[string]string{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("path not found: %s", path)
		}
		plan, err := planPath(path, info.IsDir())
		if err != nil {
			return nil, err
		}
		if prior := seen[plan.name]; prior != "" {
			return nil, fmt.Errorf("collection name collision %q: %s and %s", plan.name, prior, path)
		}
		seen[plan.name] = path
		plans = append(plans, plan)
	}
	return plans, nil
}

func planPath(path string, isDir bool) (collectionPlan, error) {
	if !isDir {
		name := basename(path)
		return collectionPlan{name: name, sourcePath: path, specs: []specFile{{basename: name, path: path}}}, nil
	}
	return directoryPlan(path)
}

func directoryPlan(path string) (collectionPlan, error) {
	files, err := yamlFiles(path)
	if err != nil {
		return collectionPlan{}, err
	}
	if len(files) == 0 {
		return collectionPlan{}, fmt.Errorf("no *.yaml or *.yml files in %s", path)
	}
	plan := collectionPlan{name: collectionName(path), sourcePath: path}
	seen := map[string]string{}
	for _, file := range files {
		name := basename(file)
		if prior := seen[name]; prior != "" {
			return collectionPlan{}, fmt.Errorf("%w in collection %q: %s and %s", errSpecCollision, plan.name, prior, file)
		}
		seen[name] = file
		plan.specs = append(plan.specs, specFile{basename: name, path: file})
	}
	return plan, nil
}

func yamlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && isYAML(entry.Name()) {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func isYAML(name string) bool {
	extension := strings.ToLower(filepath.Ext(name))
	return extension == ".yaml" || extension == ".yml"
}
func basename(path string) string { return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) }
func collectionName(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	name := filepath.Base(abs)
	if name == "data" {
		name = filepath.Base(filepath.Dir(abs))
	}
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "root"
	}
	return name
}
