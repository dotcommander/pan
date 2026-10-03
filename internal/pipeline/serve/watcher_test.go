package serve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatcherPreservesLastGoodOnDiscoveryFailure(t *testing.T) {
	t.Parallel()
	parent := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(parent, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := collectionPlan{name: "fixture", sourcePath: filepath.Join(parent, "directory")}
	states := map[string]fileState{"previous": {ref: specRef{collection: "fixture", name: "page"}}}
	wanted, preserve := desiredRefs([]collectionPlan{plan})
	if !preserve["fixture"] {
		t.Fatal("transient discovery failure did not preserve collection")
	}
	removeMissing(newCollectionSet(), states, wanted, preserve)
	if len(states) != 1 {
		t.Fatal("last-good state was removed")
	}
}

func TestWatcherRemovesConfirmedAbsentCollection(t *testing.T) {
	t.Parallel()
	plan := collectionPlan{name: "fixture", sourcePath: filepath.Join(t.TempDir(), "absent")}
	states := map[string]fileState{"previous": {ref: specRef{collection: "fixture", name: "page"}}}
	wanted, preserve := desiredRefs([]collectionPlan{plan})
	removeMissing(newCollectionSet(), states, wanted, preserve)
	if len(states) != 0 {
		t.Fatal("confirmed absent state was retained")
	}
}

func TestWatcherRemovesConfirmedEmptyDirectory(t *testing.T) {
	t.Parallel()
	plan := collectionPlan{name: "fixture", sourcePath: t.TempDir()}
	states := map[string]fileState{"previous": {ref: specRef{collection: "fixture", name: "page"}}}
	wanted, preserve := desiredRefs([]collectionPlan{plan})
	removeMissing(newCollectionSet(), states, wanted, preserve)
	if len(states) != 0 {
		t.Fatal("empty successful listing did not remove missing entries")
	}
}
