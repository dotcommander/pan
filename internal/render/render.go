// Package render renders versioned analysis envelopes deterministically.
// Both output formats are pure functions of the canonicalized envelope: equal
// envelopes always produce byte-identical output.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// maxSkippedRendered bounds the skipped section of the text summary; JSON
// output always carries the full canonicalized list.
const maxSkippedRendered = 10

// Envelope is the versioned result envelope shared by every pan command.
// Values are treated as immutable: NewEnvelope and Write canonicalize
// defensive copies, so mutating caller slices after construction never
// changes rendered output.
type Envelope struct {
	SchemaVersion string               `json:"schema_version"`
	Command       []string             `json:"command"`
	Repository    string               `json:"repository"`
	Analysis      analyze.Status       `json:"analysis"`
	Result        any                  `json:"result"`
	Diagnostics   []analyze.Diagnostic `json:"diagnostics,omitempty"`
}

// NewEnvelope builds a versioned envelope from analysis outputs, stamping it
// with the analysis schema version and canonicalizing all slice state.
func NewEnvelope(command []string, repository string, analysis analyze.Status, result any, diagnostics []analyze.Diagnostic) Envelope {
	return canonicalize(Envelope{
		Command:     command,
		Repository:  repository,
		Analysis:    analysis,
		Result:      result,
		Diagnostics: diagnostics,
	})
}

// Write renders envelope to w. format "json" produces indented JSON; any
// other format produces the deterministic text summary.
func Write(w io.Writer, format string, envelope Envelope) error {
	e := canonicalize(envelope)
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(e)
	}
	return writeText(w, e)
}

// canonicalize returns a defensive copy with a stamped schema version and
// sorted, deduplicated diagnostics and status lists.
func canonicalize(e Envelope) Envelope {
	if e.SchemaVersion == "" {
		e.SchemaVersion = analyze.SchemaVersion
	}
	e.Command = slices.Clone(e.Command)
	e.Analysis = canonicalStatus(e.Analysis)
	e.Diagnostics = canonicalDiagnostics(e.Diagnostics)
	return e
}

func canonicalStatus(status analyze.Status) analyze.Status {
	status.Limits = sortedUnique(status.Limits)
	status.Skipped = sortedUnique(status.Skipped)
	if status.SkippedCount < len(status.Skipped) {
		status.SkippedCount = len(status.Skipped)
	}
	return status
}

func canonicalDiagnostics(diags []analyze.Diagnostic) []analyze.Diagnostic {
	if len(diags) == 0 {
		return nil
	}
	out := slices.Clone(diags)
	sort.Slice(out, func(i, j int) bool { return diagLess(out[i], out[j]) })
	seen := make(map[string]struct{}, len(out))
	deduped := out[:0]
	for _, d := range out {
		key := diagKey(d)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, d)
	}
	return deduped
}

func diagLess(a, b analyze.Diagnostic) bool {
	ap, al, bp, bl := "", 0, "", 0
	if a.Location != nil {
		ap, al = a.Location.Path, a.Location.Line
	}
	if b.Location != nil {
		bp, bl = b.Location.Path, b.Location.Line
	}
	if ap != bp {
		return ap < bp
	}
	if al != bl {
		return al < bl
	}
	if a.Level != b.Level {
		return a.Level < b.Level
	}
	return a.Message < b.Message
}

func diagKey(d analyze.Diagnostic) string {
	path, line := "", 0
	if d.Location != nil {
		path, line = d.Location.Path, d.Location.Line
	}
	return fmt.Sprintf("%s|%s|%d|%s", d.Level, path, line, d.Message)
}

func writeText(w io.Writer, e Envelope) error {
	var b strings.Builder
	fmt.Fprintf(&b, "pan %s\n", e.SchemaVersion)
	fmt.Fprintf(&b, "command: %s\n", strings.Join(e.Command, " "))
	fmt.Fprintf(&b, "repository: %s\n", e.Repository)
	fmt.Fprintf(&b, "complete: %t\n", e.Analysis.Complete)
	if len(e.Analysis.Limits) > 0 {
		fmt.Fprintf(&b, "limits: %s\n", strings.Join(e.Analysis.Limits, ", "))
	}
	writeSkipped(&b, e.Analysis)
	if len(e.Diagnostics) > 0 {
		fmt.Fprintf(&b, "diagnostics: %d\n", len(e.Diagnostics))
	}
	b.WriteString("\n")
	b.WriteString(resultText(e.Result))
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeSkipped(b *strings.Builder, status analyze.Status) {
	if status.SkippedCount == 0 {
		return
	}
	fmt.Fprintf(b, "skipped: %d\n", status.SkippedCount)
	shown := min(len(status.Skipped), maxSkippedRendered)
	for _, entry := range status.Skipped[:shown] {
		fmt.Fprintf(b, "  - %s\n", entry)
	}
	if extra := len(status.Skipped) - shown; extra > 0 {
		fmt.Fprintf(b, "  - ... (%d more)\n", extra)
	}
}

func resultText(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("render error: %v", err)
	}
	return string(data)
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	slices.Sort(out)
	return slices.Compact(out)
}
