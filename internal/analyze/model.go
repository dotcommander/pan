// Package analyze builds versioned, immutable repository snapshots under
// explicit bounds. A Snapshot returned by Build is finalized: its contents
// are sorted, deduplicated, capped, and stamped with SchemaVersion. Treat it
// as read-only; derive updated copies instead of mutating shared values.
package analyze

import "time"

// SchemaVersion is the analysis schema stamped on every snapshot and on the
// result envelope rendered by the render package.
const SchemaVersion = "pan/v1"

// AnalyzerRevision changes when compiled evidence semantics change without a
// public envelope schema change. Cache entries must match this value before
// their stored snapshot can be reused.
const AnalyzerRevision = "reference-queries/v1"

const (
	languageGo        = "go"
	diagnosticWarning = "warning"
)

// Location names one repository-relative path and line.
type Location struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

// Diagnostic is one analysis-time observation attached to a snapshot.
type Diagnostic struct {
	Level    string    `json:"level"`
	Message  string    `json:"message"`
	Location *Location `json:"location,omitempty"`
}

// Status reports how complete a bounded analysis pass was.
// Complete is false only when a bound truncated discovery or a filesystem
// error skipped content; deliberate scope skips (excluded directories,
// symlinks, irregular files) are recorded in Skipped without flipping
// Complete.
type Status struct {
	Complete     bool          `json:"complete"`
	Limits       []string      `json:"limits,omitempty"`
	Skipped      []string      `json:"skipped,omitempty"`
	SkippedCount int           `json:"skipped_count,omitempty"`
	Snapshot     *SnapshotInfo `json:"snapshot,omitempty"`
}

// SnapshotInfo identifies the captured evidence behind a result.
type SnapshotInfo struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Freshness string    `json:"freshness"`
	CheckedAt time.Time `json:"checked_at"`
}

// File is one discovered repository file with its identity facts.
type File struct {
	Path      string `json:"path"`
	Language  string `json:"language"`
	Size      int64  `json:"size"`
	Generated bool   `json:"generated"`
}

// Symbol is one top-level declaration extracted from a source file.
type Symbol struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Signature string   `json:"signature,omitempty"`
	Receiver  string   `json:"receiver,omitempty"`
	Package   string   `json:"package,omitempty"`
	Exported  bool     `json:"exported,omitempty"`
	Doc       string   `json:"doc,omitempty"`
	EndLine   int      `json:"end_line,omitempty"`
	Location  Location `json:"location"`
}

// Edge is one relationship between two named nodes with the confidence
// label describing how it was observed.
type Edge struct {
	From       string   `json:"from"`
	To         string   `json:"to"`
	Kind       string   `json:"kind"`
	Symbol     string   `json:"symbol,omitempty"`
	Confidence string   `json:"confidence"`
	Location   Location `json:"location"`
}

// Truncation records one bounded output surface: how many entries were
// shown of how many existed, and why the remainder was cut. Every capped
// result section carries one so consumers can distinguish "all evidence"
// from "evidence omitted to stay bounded".
type Truncation struct {
	Field  string `json:"field"`
	Shown  int    `json:"shown"`
	Total  int    `json:"total"`
	Reason string `json:"reason"`
}

// ReadNext names one bounded source span worth inspecting next, with a
// short note explaining its role in the surrounding result.
type ReadNext struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Note  string `json:"note"`
}

// Evidence confidence labels shared across commands. They describe the kind
// of observation behind a reported fact, never a quality guarantee.
const (
	ConfidenceConfirmed = "confirmed" // structural fact from Go AST or file identity
	ConfidenceSyntactic = "syntactic" // parsed structure without type resolution
	ConfidenceLexical   = "lexical"   // by-name matching that may be coincidental
	ConfidenceHeuristic = "heuristic" // naming or shape convention, may mislead
)

// Snapshot is the immutable result of one bounded analysis pass. Instructions
// is left empty by Build; the calling service layers instruction discovery on
// top of the finalized snapshot.
type Snapshot struct {
	SchemaVersion  string               `json:"schema_version"`
	Root           string               `json:"root"`
	Files          []File               `json:"files"`
	Symbols        []Symbol             `json:"symbols"`
	Edges          []Edge               `json:"edges"`
	Instructions   []string             `json:"instructions"`
	Status         Status               `json:"status"`
	Diagnostics    []Diagnostic         `json:"diagnostics,omitempty"`
	Captured       map[string][]byte    `json:"-"`
	CapturedStamps map[string]FileStamp `json:"-"`
}

// Stamps returns a defensive copy of the metadata set verified around capture.
func (s Snapshot) Stamps() map[string]FileStamp {
	out := make(map[string]FileStamp, len(s.CapturedStamps))
	for path, stamp := range s.CapturedStamps {
		out[path] = stamp
	}
	return out
}

// Source returns a defensive copy of captured repository bytes.
func (s Snapshot) Source(path string) ([]byte, bool) {
	b, ok := s.Captured[path]
	return append([]byte(nil), b...), ok
}
