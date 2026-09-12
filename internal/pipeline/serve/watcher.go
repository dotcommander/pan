package serve

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type fileState struct {
	ref         specRef
	fingerprint [sha256.Size]byte
}

func loadPlans(set *collectionSet, plans []collectionPlan) (map[string]fileState, error) {
	current := make(map[string]fileState)
	for _, plan := range plans {
		for _, file := range plan.specs {
			absolute, err := filepath.Abs(file.path)
			if err != nil {
				absolute = file.path
			}
			data, err := os.ReadFile(absolute)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", absolute, err)
			}
			state := fileState{ref: specRef{collection: plan.name, name: file.basename, sourcePath: absolute, collectionSource: plan.sourcePath}, fingerprint: sha256.Sum256(data)}
			if err := set.add(state.ref); err != nil {
				return nil, err
			}
			current[absolute] = state
		}
	}
	return current, nil
}

func poll(ctx context.Context, set *collectionSet, plans []collectionPlan, states map[string]fileState) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			states = refresh(set, plans, states)
		}
	}
}

func refresh(set *collectionSet, plans []collectionPlan, states map[string]fileState) map[string]fileState {
	wanted, preserve := desiredRefs(plans)
	removeMissing(set, states, wanted, preserve)
	loadChanged(set, states, wanted)
	return states
}

func desiredRefs(plans []collectionPlan) (map[string]specRef, map[string]bool) {
	wanted := make(map[string]specRef)
	preserve := make(map[string]bool)
	for _, plan := range plans {
		files, ok := refreshedFiles(plan, preserve)
		if !ok {
			continue
		}
		for _, file := range files {
			absolute := absolutePath(file.path)
			wanted[absolute] = specRef{collection: plan.name, name: file.basename, sourcePath: absolute, collectionSource: plan.sourcePath}
		}
	}
	return wanted, preserve
}

func refreshedFiles(plan collectionPlan, preserve map[string]bool) ([]specFile, bool) {
	info, err := os.Stat(plan.sourcePath)
	if err != nil {
		return nil, false
	}
	if !info.IsDir() {
		return plan.specs, true
	}
	candidate, err := directoryPlan(plan.sourcePath)
	if err == nil {
		return candidate.specs, true
	}
	if errors.Is(err, errSpecCollision) {
		preserve[plan.name] = true
	}
	return nil, false
}

func removeMissing(set *collectionSet, states map[string]fileState, wanted map[string]specRef, preserve map[string]bool) {
	for path, old := range states {
		if preserve[old.ref.collection] {
			continue
		}
		if _, ok := wanted[path]; !ok {
			set.remove(old.ref)
			delete(states, path)
		}
	}
}

func loadChanged(set *collectionSet, states map[string]fileState, wanted map[string]specRef) {
	for path, ref := range wanted {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fingerprint := sha256.Sum256(data)
		if old, ok := states[path]; ok && old.fingerprint == fingerprint && old.ref == ref {
			continue
		}
		state := fileState{ref: ref, fingerprint: fingerprint}
		// Keep the previous page on a malformed partial write. Recording this
		// fingerprint avoids retrying an unchanged invalid file every poll.
		_ = set.add(ref)
		states[path] = state
	}
}

func absolutePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
