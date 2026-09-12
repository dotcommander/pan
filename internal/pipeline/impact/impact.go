// Package impact analyzes the blast radius and downstream consequences
// of modifying a file, stage, or symbol within a Pan pipeline spec.
package impact

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

// TargetType indicates what kind of target was matched.
type TargetType string

// TargetType values classify query matches.
const (
	// TargetTypeFile identifies a matching source file.
	TargetTypeFile   TargetType = "file"
	TargetTypeStage  TargetType = "stage"
	TargetTypeSymbol TargetType = "symbol"
	TargetTypeStore  TargetType = "store"
)

// Match holds information about what was matched for the query target.
type Match struct {
	Type   TargetType `json:"type"`
	Name   string     `json:"name"`
	Detail string     `json:"detail,omitempty"`
}

// AffectedPhase describes a phase in the blast radius.
type AffectedPhase struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind,omitempty"`
	Command     string   `json:"command,omitempty"`      // Empty if in prelude / single pipeline
	Ordinal     int      `json:"ordinal"`                // 1-based order in its pipeline
	Reason      string   `json:"reason"`                 // "direct-match", "downstream-flow", "store-consumer"
	Stages      []string `json:"stages,omitempty"`       // Stages in this phase
	StoresRead  []string `json:"stores_read,omitempty"`  // Stores read by this phase
	StoresWrite []string `json:"stores_write,omitempty"` // Stores written by this phase
}

// Result is the complete blast radius impact assessment.
type Result struct {
	Target           string          `json:"target"`
	Matches          []Match         `json:"matches"`
	DirectPhases     []AffectedPhase `json:"direct_phases"`
	DownstreamPhases []AffectedPhase `json:"downstream_phases"`
	AffectedStores   []string        `json:"affected_stores"`
	AffectedCommands []string        `json:"affected_commands"`
	TotalPhases      int             `json:"total_phases"`
	Severity         string          `json:"severity"` // "isolated", "moderate", "high", "critical"
	Summary          string          `json:"summary"`
}

func slicesContainsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func rankedFileMatches(root, normTarget, rawTarget string, ranked []symbols.RankedFile, modeled map[string]bool) []string {
	files := make(map[string]bool)
	for _, rf := range ranked {
		if rf.FileSymbols == nil {
			continue
		}
		rel := normalizePath(rf.Path, root)
		if fileMatches(rel, normTarget, rawTarget) && !modeled[filepath.Clean(rel)] {
			files[rel] = true
		}
	}
	return sortedKeys(files)
}

func storeNameMatches(name, target string) bool {
	if strings.EqualFold(name, target) {
		return true
	}
	semanticTarget := strings.ToLower(strings.TrimSpace(target))
	semanticTarget = strings.TrimSuffix(semanticTarget, "path")
	semanticTarget = strings.TrimSuffix(semanticTarget, "file")
	if semanticTarget != "" && strings.HasPrefix(strings.ToLower(name), semanticTarget+":") {
		return true
	}
	return strings.EqualFold(filepath.Base(name), target) ||
		strings.EqualFold(strings.TrimPrefix(filepath.Ext(name), "."), target) ||
		strings.EqualFold(name[strings.LastIndex(name, ".")+1:], target)
}

func phaseHasFile(files []string, target string) bool {
	for _, file := range files {
		if fileMatches(file, target, target) {
			return true
		}
	}
	return false
}

const (
	accessRead = "read"
	languageGo = "go"
)

func isReadAccess(access string) bool {
	switch strings.ToLower(strings.TrimSpace(access)) {
	case "r", accessRead:
		return true
	default:
		return false
	}
}

func sortedKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// JSON returns formatted JSON representation of the Result.
func (r *Result) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Helper methods

func normalizePath(path, root string) string {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		if rel, err := filepath.Rel(root, cleaned); err == nil {
			return rel
		}
	}
	return cleaned
}

func fileMatches(phaseFile, normTarget, rawTarget string) bool {
	pf := filepath.Clean(phaseFile)
	nt := filepath.Clean(normTarget)
	rt := filepath.Clean(rawTarget)

	if pf == nt || pf == rt {
		return true
	}
	if strings.HasPrefix(nt, pf) || strings.HasPrefix(rt, pf) {
		return true
	}
	if strings.HasSuffix(pf, nt) || strings.HasSuffix(pf, rt) {
		return true
	}
	// Basename match
	if filepath.Base(pf) == filepath.Base(nt) || filepath.Base(pf) == filepath.Base(rt) {
		return true
	}
	return false
}

func expandPhaseFiles(files []string, root string) []string {
	var out []string
	for _, f := range files {
		clean := filepath.Clean(f)
		out = append(out, clean)
		out = append(out, expandGlobFiles(clean, root)...)
	}
	return out
}

func expandGlobFiles(path, root string) []string {
	if !spec.HasGlobMeta(path) {
		return nil
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	matches, err := filepath.Glob(abs)
	if err != nil {
		return nil
	}
	files := make([]string, 0, len(matches))
	for _, match := range matches {
		if relative, err := filepath.Rel(root, match); err == nil {
			files = append(files, relative)
		}
	}
	return files
}

func extractAllStageLabels(stages []spec.Stage) []string {
	var out []string
	for _, st := range stages {
		switch {
		case st.Chip != nil:
			out = append(out, st.Chip.Label)
		case st.Fork != nil:
			out = append(out, st.Fork.Gate)
			for _, b := range st.Fork.Branches {
				if b.Label != "" {
					out = append(out, b.Label)
				}
			}
		case st.Fanout != nil:
			out = append(out, st.Fanout.Gate)
			for _, t := range st.Fanout.Targets {
				if t.Label != "" {
					out = append(out, t.Label)
				}
			}
		case st.External != nil:
			out = append(out, st.External.Label)
		}
	}
	return out
}

func stageNamesEqual(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func phaseStoreLabels(phaseName string, stages []string) []string {
	if len(stages) > 0 {
		return stages
	}
	return []string{phaseName}
}

func storesForPhase(phaseStages []string, stores []spec.Store) (reads, writes []string) {
	for _, st := range stores {
		for _, w := range st.Writers {
			for _, ps := range phaseStages {
				if stageNamesEqual(ps, w.Stage) {
					switch strings.ToLower(strings.TrimSpace(w.Access)) {
					case "r", accessRead:
						reads = append(reads, st.Name)
					case "w", "write":
						writes = append(writes, st.Name)
					default:
						// Default assumption in Pan is "written by"
						writes = append(writes, st.Name)
					}
				}
			}
		}
	}
	return dedupeStrings(reads), dedupeStrings(writes)
}

func dedupeMatches(matches []Match) []Match {
	seen := make(map[string]bool)
	var out []Match
	for _, m := range matches {
		k := fmt.Sprintf("%s:%s:%s", m.Type, m.Name, m.Detail)
		if !seen[k] {
			seen[k] = true
			out = append(out, m)
		}
	}
	return out
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	sort.Strings(out)
	return out
}
