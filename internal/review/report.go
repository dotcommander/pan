// Package review composes deterministic audit reports from scan packets.
// Composition never re-inspects the repository: every input packet is
// already derived by the scan package, so a report is a pure function of
// those packets and runs with no provider, model, or network access.
package review

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/scan"
)

// maxReadQueue bounds the read queue even when top is zero (list all).
const maxReadQueue = 100

// ReadItem is one deterministic read recommendation with its rank, content
// identity, score, cull lane, and reasons.
type ReadItem struct {
	Rank       int      `json:"rank"`
	EvidenceID string   `json:"evidence_id"`
	Path       string   `json:"path"`
	Score      int      `json:"score"`
	Lane       string   `json:"lane"`
	Why        []string `json:"why"`
}

// Report is the full deterministic audit report: every scan packet plus a
// merged read queue ranking where to start reading. CullLedger is appended
// only when the caller requests a cull pass.
type Report struct {
	Overview   scan.OverviewReport `json:"overview"`
	Risks      scan.RiskReport     `json:"risks"`
	Surface    scan.SurfaceReport  `json:"surface"`
	Effects    scan.EffectsReport  `json:"effects"`
	Hygiene    scan.HygieneReport  `json:"hygiene"`
	Changes    scan.ChangesReport  `json:"changes"`
	ReadQueue  []ReadItem          `json:"read_queue"`
	Rationale  []Rationale         `json:"rationale,omitempty"`
	CullLedger *CullLedger         `json:"cull_ledger,omitempty"`
}

// readCandidate accumulates ranking signals for one path.
type readCandidate struct {
	path     string
	priority int
	churn    int
	why      []string
}

// Packets carries the scan packets Compose merges into one report. Every
// packet is already derived by the scan package, so composition is a pure
// function of these values.
type Packets struct {
	Overview scan.OverviewReport
	Risks    scan.RiskReport
	Surface  scan.SurfaceReport
	Effects  scan.EffectsReport
	Hygiene  scan.HygieneReport
	Changes  scan.ChangesReport
	Paths    []string
}

// Compose merges the packets into one report and derives the read queue:
// risk-ranked files first, then hygiene drift and churn evidence, merged
// by path with all reasons preserved. top > 0 caps the queue.
func Compose(packets Packets, top int) Report {
	candidates := collectReadCandidates(packets.Risks, packets.Hygiene, packets.Changes, packets.Paths)
	return Report{
		Overview:  packets.Overview,
		Risks:     packets.Risks,
		Surface:   packets.Surface,
		Effects:   packets.Effects,
		Hygiene:   packets.Hygiene,
		Changes:   packets.Changes,
		ReadQueue: buildReadQueue(candidates, top),
	}
}

// collectReadCandidates merges risk, hygiene, and churn signals into one
// candidate per path, with all reasons preserved.
func collectReadCandidates(risks scan.RiskReport, hygiene scan.HygieneReport, changes scan.ChangesReport, paths []string) map[string]*readCandidate {
	candidates := map[string]*readCandidate{}
	get := func(path string) *readCandidate {
		entry, ok := candidates[path]
		if !ok {
			entry = &readCandidate{path: path}
			candidates[path] = entry
		}
		return entry
	}
	collectRiskCandidates(risks, get)
	collectHygieneCandidates(hygiene, get)
	collectChurnCandidates(changes, get)
	collectFilesystemCandidates(paths, candidates)
	return candidates
}

func collectFilesystemCandidates(paths []string, candidates map[string]*readCandidate) {
	for _, filePath := range paths {
		if _, exists := candidates[filePath]; !exists {
			candidates[filePath] = &readCandidate{path: filePath, why: []string{"low-signal"}}
		}
	}
}

// collectRiskCandidates adds each risk file's score and lanes.
func collectRiskCandidates(risks scan.RiskReport, get func(string) *readCandidate) {
	for _, risk := range risks.Files {
		entry := get(risk.Path)
		entry.priority += risk.Score
		for _, lane := range risk.Lanes {
			entry.why = appendUnique(entry.why, "risk:"+lane)
		}
	}
}

// collectHygieneCandidates adds hygiene drift weighted by severity.
func collectHygieneCandidates(hygiene scan.HygieneReport, get func(string) *readCandidate) {
	for _, issue := range hygiene.Issues {
		entry := get(issue.Path)
		entry.priority += hygienePriority(issue.Severity)
		entry.why = appendUnique(entry.why, "hygiene:"+issue.ID)
	}
}

// hygienePriority weights one hygiene severity; unknown severities add no
// weight but their reason still surfaces on the row.
func hygienePriority(severity string) int {
	switch severity {
	case "high":
		return 1000
	case "medium":
		return 500
	default:
		return 0
	}
}

// collectChurnCandidates records per-file commit churn when git history is
// available.
func collectChurnCandidates(changes scan.ChangesReport, get func(string) *readCandidate) {
	if !changes.GitAvailable {
		return
	}
	for _, file := range changes.TopFiles {
		entry := get(file.Path)
		entry.churn = file.Commits
		entry.priority += file.Commits
		entry.why = appendUnique(entry.why, fmt.Sprintf("churn:%d commits in last %d days", file.Commits, changes.Days))
		if file.FixTouches > 0 {
			entry.priority += 2 * file.FixTouches
			entry.why = appendUnique(entry.why, fmt.Sprintf("history:%d fix touches", file.FixTouches))
		}
	}
}

// buildReadQueue ranks the candidates, caps the queue at top (bounded by
// maxReadQueue), derives each row's evidence identity, and classifies its
// cull lane.
func buildReadQueue(candidates map[string]*readCandidate, top int) []ReadItem {
	queue := make([]readCandidate, 0, len(candidates))
	for _, entry := range candidates {
		queue = append(queue, *entry)
	}
	slices.SortFunc(queue, compareReadCandidates)
	limit := top
	if limit <= 0 || limit > maxReadQueue {
		limit = maxReadQueue
	}
	if len(queue) > limit {
		queue = queue[:limit]
	}
	items := make([]ReadItem, 0, len(queue))
	for i, entry := range queue {
		item := ReadItem{Rank: i + 1, Path: entry.path, Score: entry.priority, Why: entry.why}
		item.EvidenceID = EvidenceIdentity(item)
		items = append(items, item)
	}
	for i, disposition := range CullDispositions(items) {
		items[i].Lane = disposition.Lane
	}
	return items
}

// compareReadCandidates orders by priority, then churn, then path so the
// queue is deterministic.
func compareReadCandidates(a, b readCandidate) int {
	if a.priority != b.priority {
		return b.priority - a.priority
	}
	if a.churn != b.churn {
		return b.churn - a.churn
	}
	return strings.Compare(a.path, b.path)
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
