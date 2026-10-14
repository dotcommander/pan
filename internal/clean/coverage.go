package clean

import "sort"

// Coverage records eligible content omitted from a cleanup walk.
// It is carried internally to presentation, not added to clean result schemas.
// Scores and negative findings describe inspected paths only when incomplete.
type Coverage struct {
	Complete     bool
	Limits       []string
	Skipped      []string
	SkippedCount int
}

// CleanCoverage exposes the internal coverage sidecar to result consumers.
func (c Coverage) CleanCoverage() Coverage { return c }

type walkInventory struct {
	Files    []FileInfo
	Coverage Coverage
}

func (w *walker) coverage() Coverage {
	coverage := Coverage{Complete: true}
	for abs, rel := range w.depthSkipped {
		if !isUnderAny(abs, w.nestedRepos) {
			coverage.Skipped = append(coverage.Skipped, rel)
		}
	}
	if len(coverage.Skipped) != 0 {
		coverage.Complete = false
		coverage.Limits = []string{"max_depth"}
		coverage.SkippedCount = len(coverage.Skipped)
		sort.Strings(coverage.Skipped)
	}
	return coverage
}
