package scan

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// Truncation accounts for every deterministic packet cap.
type Truncation struct {
	Field  string `json:"field"`
	Shown  int    `json:"shown"`
	Total  int    `json:"total"`
	Reason string `json:"reason"`
}

// SurfaceHit is one static public-surface lead.
type SurfaceHit struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	Line     int    `json:"line"`
	Evidence string `json:"evidence"`
}

// SurfaceFile groups surface hits by source file.
type SurfaceFile struct {
	Path  string       `json:"path"`
	Kinds []string     `json:"kinds"`
	Hits  []SurfaceHit `json:"hits"`
}

// SurfaceReport is the deterministic public-surface inventory: commands,
// flags, environment variables, config keys, schema fields, routes, and
// output writes discovered by bounded line inspection of parsed source.
type SurfaceReport struct {
	Files              []SurfaceFile  `json:"files"`
	FilesOmittedReason string         `json:"files_omitted_reason,omitempty"`
	Counts             map[string]int `json:"counts"`
	Truncations        []Truncation   `json:"truncations,omitempty"`
}

// surfacePattern is one bounded line pattern producing a named hit.
type surfacePattern struct {
	Kind string
	Re   *regexp.Regexp
}

// surfaceTable returns the bounded line patterns producing named hits,
// built per packet run so the package owns no mutable global tables.
func surfaceTable() []surfacePattern {
	return []surfacePattern{
		{Kind: "flag", Re: regexp.MustCompile(`\bflag\.(?:String|Bool|Int|Duration|Float64)\(\s*"([^"]+)"`)},
		{Kind: "env-var", Re: regexp.MustCompile(`\bos\.(?:Getenv|LookupEnv)\(\s*"([^"]+)"`)},
		{Kind: "config-key", Re: regexp.MustCompile("`[^`]*yaml:\"([^\" ,]+)[^\"]*\"[^`]*`")},
		{Kind: "schema-field", Re: regexp.MustCompile("`[^`]*json:\"([^\" ,]+)[^\"]*\"[^`]*`")},
		{Kind: "route", Re: regexp.MustCompile(`\bhttp\.(?:Handle|HandleFunc)\(\s*"([^"]+)"`)},
		{Kind: "output", Re: regexp.MustCompile(`\b(?:fmt\.Fprint\w*|os\.Std(?:out|err)|json\.NewEncoder)\(`)},
	}
}

// Surface extracts the user-facing surface from non-test source files in the
// snapshot. top > 0 caps the returned file list; counts and truncations
// always reflect every discovered hit.
func Surface(ctx context.Context, snap analyze.Snapshot, top int) (SurfaceReport, error) {
	patterns := surfaceTable()
	var files []SurfaceFile
	counts := map[string]int{}
	var truncations []Truncation
	for _, file := range snap.Files {
		if file.Language == languageUnknown || isTestPath(file.Path) {
			continue
		}
		lines, truncated, err := readLines(ctx, path.Join(snap.Root, filepathFromSlash(file.Path)))
		if err != nil {
			return SurfaceReport{}, fmt.Errorf("scan surface source %s: %w", file.Path, err)
		}
		hits := scanSurfaceLines(lines, patterns)
		if len(hits) == 0 {
			continue
		}
		for _, hit := range hits {
			counts[hit.Kind]++
		}
		sf := SurfaceFile{Path: file.Path, Hits: capHits(hits)}
		if len(hits) > perFileHitCap {
			sf.Kinds = hitKinds(hits)
			truncations = append(truncations, Truncation{
				Field: "files[" + file.Path + "].hits", Shown: perFileHitCap, Total: len(hits), Reason: "surface per-file cap",
			})
		} else {
			sf.Kinds = hitKinds(hits)
		}
		if truncated {
			truncations = append(truncations, Truncation{
				Field: "files[" + file.Path + "].lines", Shown: len(lines), Total: len(lines) + 1, Reason: "line bound reached; tail not inspected",
			})
		}
		files = append(files, sf)
	}
	slices.SortFunc(files, func(a, b SurfaceFile) int {
		if len(a.Hits) != len(b.Hits) {
			return len(b.Hits) - len(a.Hits)
		}
		return strings.Compare(a.Path, b.Path)
	})
	total := len(files)
	if top > 0 && len(files) > top {
		files = files[:top]
	}
	report := SurfaceReport{Files: files, Counts: counts, Truncations: truncations}
	switch {
	case len(files) == 0:
		report.FilesOmittedReason = "no surface data extracted from scanned files"
	case len(files) < total:
		report.FilesOmittedReason = fmt.Sprintf("showing %d of %d files; truncated by --top", len(files), total)
	}
	return report, nil
}

// scanSurfaceLines inspects bounded lines for Kong struct tags and the
// pattern table. Kong command and flag declarations are recognized from
// their struct tags before the generic patterns run.
func scanSurfaceLines(lines []sourceLine, patterns []surfacePattern) []SurfaceHit {
	var hits []SurfaceHit
	for _, line := range lines {
		if hit, ok := kongSurfaceHit(line); ok {
			hits = append(hits, hit)
		}
		for _, pattern := range patterns {
			match := pattern.Re.FindStringSubmatch(line.text)
			if match == nil {
				continue
			}
			name := ""
			if len(match) > 1 {
				name = match[1]
			}
			hits = append(hits, SurfaceHit{Kind: pattern.Kind, Name: name, Line: line.number, Evidence: evidence(line.text)})
		}
	}
	return hits
}

// kongSurfaceHit recognizes Kong command and flag declarations from struct
// tags: a `cmd:""` tag declares a command; name/help/short/hidden/arg tags
// on an exported field declare a flag or argument.
func kongSurfaceHit(line sourceLine) (SurfaceHit, bool) {
	tagStart := strings.IndexByte(line.text, '`')
	tagEnd := strings.LastIndexByte(line.text, '`')
	if tagStart < 0 || tagEnd <= tagStart {
		return SurfaceHit{}, false
	}
	fields := strings.Fields(line.text[:tagStart])
	if len(fields) < 2 || !startsWithUpper(fields[0]) {
		return SurfaceHit{}, false
	}
	tag := reflect.StructTag(line.text[tagStart+1 : tagEnd])
	_, command := tag.Lookup("cmd")
	_, argument := tag.Lookup("arg")
	_, documented := tag.Lookup("help")
	_, hidden := tag.Lookup("hidden")
	if hidden {
		return SurfaceHit{}, false
	}
	_, named := tag.Lookup("name")
	_, shortened := tag.Lookup("short")
	if !command && (argument || (!documented && !hidden && !named && !shortened)) {
		return SurfaceHit{}, false
	}
	name := tag.Get("name")
	if name == "" {
		name = kebabName(fields[0])
	}
	kind := "flag"
	if command {
		kind = "command"
	}
	return SurfaceHit{Kind: kind, Name: name, Line: line.number, Evidence: evidence(line.text)}, true
}

func startsWithUpper(field string) bool {
	if field == "" {
		return false
	}
	r := []rune(field)[0]
	return r == '_' || (r >= 'A' && r <= 'Z')
}

func capHits(hits []SurfaceHit) []SurfaceHit {
	if len(hits) > perFileHitCap {
		return hits[:perFileHitCap:perFileHitCap]
	}
	return hits
}

func hitKinds(hits []SurfaceHit) []string {
	seen := map[string]bool{}
	var out []string
	for _, hit := range hits {
		if seen[hit.Kind] {
			continue
		}
		seen[hit.Kind] = true
		out = append(out, hit.Kind)
	}
	slices.Sort(out)
	return out
}
