package scan

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/fileclass"
)

// maxGoModBytes bounds the go.mod read used for package fan-in scoring.
const maxGoModBytes = 1 << 20

// edgeKindImports is the snapshot edge kind carrying import dependencies.
const edgeKindImports = "imports"

// FileRisk summarizes why one file deserves review attention. Scores are
// deterministic sums of matched signal weights; they rank attention, they
// are not verdicts.
type FileRisk struct {
	Path            string               `json:"path"`
	Score           int                  `json:"score"`
	ReviewPriority  int                  `json:"review_priority"`
	FileClass       string               `json:"file_class"`
	Lanes           []string             `json:"lanes"`
	Reasons         []string             `json:"reasons"`
	ScoreComponents []RiskScoreComponent `json:"score_components,omitempty"`
	ImportedBy      int                  `json:"imported_by,omitempty"`
	DependsOn       int                  `json:"depends_on,omitempty"`
	Symbols         int                  `json:"symbols,omitempty"`
	Confidence      string               `json:"confidence"`
}

// RiskScoreComponent explains one deduplicated signal and its bounded evidence.
type RiskScoreComponent struct {
	ID             string   `json:"id"`
	Lane           string   `json:"lane"`
	EvidenceKind   string   `json:"evidence_kind"`
	Confidence     string   `json:"confidence"`
	Points         int      `json:"points"`
	Matches        int      `json:"matches,omitempty"`
	TotalMatches   int      `json:"total_matches"`
	CountedMatches int      `json:"counted_matches"`
	Capped         bool     `json:"capped"`
	Locations      []int    `json:"locations,omitempty"`
	Evidence       []string `json:"evidence,omitempty"`
	Reason         string   `json:"reason"`
}

// maxRiskEvidenceRunes bounds each matched source line carried with a risk
// component. Evidence entries are paired by index with Locations.
const maxRiskEvidenceRunes = 200

// RiskLane groups the files that triggered one review lane.
type RiskLane struct {
	Name   string   `json:"name"`
	Reason string   `json:"reason"`
	Files  []string `json:"files"`
}

// RiskReport is the deterministic risk-ranked review queue.
type RiskReport struct {
	Files              []FileRisk   `json:"files"`
	FilesOmittedReason string       `json:"files_omitted_reason,omitempty"`
	Lanes              []RiskLane   `json:"lanes"`
	Analysis           RiskCoverage `json:"coverage"`
}

// RiskCoverage records the population considered by the queue.
type RiskCoverage struct {
	Complete              bool           `json:"complete"`
	Classes               []string       `json:"classes,omitempty"`
	SnapshotFiles         int            `json:"snapshot_files"`
	EligibleFiles         int            `json:"eligible_files"`
	InspectedContentFiles int            `json:"inspected_content_files"`
	ScoredFiles           int            `json:"scored_files"`
	ReturnedFiles         int            `json:"returned_files"`
	ContentTruncatedFiles int            `json:"content_truncated_files"`
	ExcludedByClass       map[string]int `json:"excluded_by_class,omitempty"`
	SkippedCount          int            `json:"skipped_count"`
	Limits                []string       `json:"limits,omitempty"`
}

// RiskOptions controls file classes admitted to the risk queue.
type RiskOptions struct{ IncludeClasses []string }

func defaultRiskOptions() RiskOptions {
	return RiskOptions{IncludeClasses: []string{string(fileclass.Production)}}
}

func (o RiskOptions) admits(class fileclass.Class) bool {
	if len(o.IncludeClasses) == 0 {
		return class == fileclass.Production
	}
	for _, want := range o.IncludeClasses {
		if want == "all" || want == string(class) || class == fileclass.Production {
			return true
		}
	}
	return false
}

// riskPathTerm is one path-shape signal (Pan-style term scoring).
type riskPathTerm struct {
	Term   string
	Weight int
	Lane   string
	Reason string
}

// riskPattern is one bounded content signal. Exclude names the bounded call
// form that must directly follow every Re occurrence on a line for that
// line to be skipped; lines with any occurrence lacking the form keep their
// match and stay flagged.
type riskPattern struct {
	ID         string
	Re         *regexp.Regexp
	Exclude    *regexp.Regexp
	Weight     int
	Lane       string
	Reason     string
	MaxMatches int
	Generic    bool
}

// riskTable bundles the deterministic signals threaded through one Risk
// run: path-shape terms, content patterns, and the structural counts
// derived from the snapshot. Building it per run keeps the package free of
// mutable global tables.
type riskTable struct {
	terms      []riskPathTerm
	patterns   []riskPattern
	importedBy map[string]int
	dependsOn  map[string]int
	symbols    map[string]int
	signatures map[string]int
}

// riskPathTermTable returns the path-shape scoring terms.
func riskPathTermTable() []riskPathTerm {
	return []riskPathTerm{
		{Term: "auth", Weight: 14, Lane: laneSecurity, Reason: "path signals authentication"},
		{Term: "token", Weight: 12, Lane: laneSecurity, Reason: "path signals token handling"},
		{Term: kindSecret, Weight: 12, Lane: laneSecurity, Reason: "path signals secret handling"},
		{Term: "password", Weight: 12, Lane: laneSecurity, Reason: "path signals credential handling"},
		{Term: termCredential, Weight: 12, Lane: laneSecurity, Reason: "path signals credential handling"},
		{Term: kindCrypto, Weight: 12, Lane: laneSecurity, Reason: "path signals cryptography"},
		{Term: "migrate", Weight: 12, Lane: laneDataIntegrity, Reason: "path signals schema migration"},
		{Term: "migration", Weight: 12, Lane: laneDataIntegrity, Reason: "path signals schema migration"},
		{Term: "handler", Weight: 8, Lane: laneAPIContracts, Reason: "path signals request handling"},
	}
}

// riskPatternTable returns the bounded content scoring patterns.
func riskPatternTable() []riskPattern {
	return []riskPattern{
		{ID: kindSubprocess, Re: regexp.MustCompile(`\bexec\.Command(?:Context)?\(`), Weight: 12, Lane: laneErrorHandling, Reason: "subprocess boundary", MaxMatches: 3},
		{ID: "unsafe", Re: regexp.MustCompile(`\bunsafe\.`), Weight: 14, Lane: laneSecurity, Reason: "unsafe memory access", MaxMatches: 3},
		{ID: "http-server", Re: regexp.MustCompile(`\bhttp\.(?:ListenAndServe|Handle(?:Func)?)\(`), Weight: 10, Lane: laneAPIContracts, Reason: "http server boundary", MaxMatches: 3},
		{ID: "http-client", Re: regexp.MustCompile(`\bhttp\.(?:Get|Post|Head)\(`), Weight: 8, Lane: laneAPIContracts, Reason: "outbound http boundary", MaxMatches: 3},
		{ID: "database-open", Re: regexp.MustCompile(`\b(?:sql\.Open|pgxpool\.New|pgx\.Connect)\(`), Weight: 10, Lane: laneDataIntegrity, Reason: "database boundary", MaxMatches: 3},
		{ID: "filesystem-write", Re: regexp.MustCompile(`\bos\.(?:WriteFile|Create(?:Temp)?|Remove(?:All)?|Rename|MkdirAll)\(`), Weight: 8, Lane: laneDataIntegrity, Reason: "filesystem write boundary", MaxMatches: 3},
		{ID: "unbounded-read", Re: regexp.MustCompile(`\bio\.ReadAll\(`), Exclude: regexp.MustCompile(`^\s*io\.LimitReader\(`), Weight: 6, Lane: lanePerformance, Reason: "unbounded read candidate", MaxMatches: 3},
		{ID: "change-marker", Re: regexp.MustCompile(`\b(?:TODO|FIXME|HACK|XXX)\b`), Weight: 4, Lane: laneBestPractices, Reason: "change marker", MaxMatches: 3, Generic: true},
		{ID: kindGoroutine, Re: regexp.MustCompile(`\bgo\s+func\(`), Weight: 6, Lane: laneLifecycleConcurrency, Reason: "goroutine launch", MaxMatches: 3},
		{ID: "panic", Re: regexp.MustCompile(`\bpanic\(`), Weight: 4, Lane: laneErrorHandling, Reason: "panic path", MaxMatches: 3},
		{ID: "detached-context", Re: regexp.MustCompile(`\bcontext\.Background\(\)`), Weight: 6, Lane: laneLifecycleConcurrency, Reason: "detached context", MaxMatches: 3},
	}
}

// genericRiskPatterns returns the content patterns safe to apply across
// languages without relying on the order of riskPatternTable.
func genericRiskPatterns(patterns []riskPattern) []riskPattern {
	var generic []riskPattern
	for _, pattern := range patterns {
		if pattern.Generic {
			generic = append(generic, pattern)
		}
	}
	return generic
}

// Snapshot-derived signal thresholds. Files meeting them earn the matching
// lane weight; thresholds mirror the Pan audit-lane triggers pan's risk
// queue is grounded in.
const (
	importedByThreshold = 5
	importedByWeight    = 15
	dependsOnThreshold  = 8
	dependsOnWeight     = 10
	symbolsThreshold    = 25
	symbolsWeight       = 8
)

// Risk ranks non-test, non-generated files by deterministic path, content,
// and structural signals. Content is inspected only for Go files already
// admitted by the snapshot bounds. top > 0 caps the returned file list;
// lanes always reflect every scored file.
func Risk(ctx context.Context, snap analyze.Snapshot, top int) (RiskReport, error) {
	return RiskWithOptions(ctx, snap, top, defaultRiskOptions())
}

// RiskWithOptions ranks admitted files using deterministic path, structural,
// and language-aware content signals.
func RiskWithOptions(ctx context.Context, snap analyze.Snapshot, top int, options RiskOptions) (RiskReport, error) {
	structure := structuralSignals(snap)
	table := riskTable{
		terms:      riskPathTermTable(),
		patterns:   riskPatternTable(),
		importedBy: structure.importedBy,
		dependsOn:  structure.dependsOn,
		symbols:    structure.symbols,
		signatures: structure.signatures,
	}
	var files []FileRisk
	laneFiles := map[string][]string{}
	considered := 0
	excluded := map[string]int{}
	inspected, contentTruncated := 0, 0
	for _, file := range snap.Files {
		class := fileclass.Classify(file.Path, file.Language, file.Generated)
		if !options.admits(class) {
			excluded[string(class)]++
			continue
		}
		considered++
		inspected++
		risk, err := riskForFile(ctx, snap, file, table)
		if err != nil {
			return RiskReport{}, err
		}
		if slices.Contains(risk.Reasons, "scan:content_truncated") {
			contentTruncated++
		}
		if risk.Score == 0 {
			continue
		}
		files = append(files, risk)
		for _, lane := range risk.Lanes {
			laneFiles[lane] = append(laneFiles[lane], risk.Path)
		}
	}
	slices.SortFunc(files, func(a, b FileRisk) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Path, b.Path)
	})
	total := len(files)
	if top > 0 && len(files) > top {
		files = files[:top]
	}
	report := RiskReport{Files: files, Lanes: buildRiskLanes(laneFiles), Analysis: RiskCoverage{Complete: snap.Status.Complete, Classes: options.IncludeClasses, SnapshotFiles: len(snap.Files), EligibleFiles: considered, InspectedContentFiles: inspected, ScoredFiles: total, ReturnedFiles: len(files), ContentTruncatedFiles: contentTruncated, ExcludedByClass: excluded, SkippedCount: snap.Status.SkippedCount, Limits: snap.Status.Limits}}
	switch {
	case len(files) == 0:
		report.FilesOmittedReason = "no files scored above the review threshold"
	case len(files) < total:
		report.FilesOmittedReason = fmt.Sprintf("showing %d of %d scored files; truncated by --top", len(files), total)
	}
	return report, nil
}

// riskForFile scores one file from the entrypoint, path-term, structural,
// and content signals in table.
func riskForFile(ctx context.Context, snap analyze.Snapshot, file analyze.File, table riskTable) (FileRisk, error) {
	lower := strings.ToLower(file.Path)
	risk := FileRisk{Path: file.Path, FileClass: string(fileclass.Classify(file.Path, file.Language, file.Generated)), Confidence: evidenceHeuristic}
	seen := map[string]int{}
	add := func(points int, lane, reason string) {
		matches := seen[reason]
		seen[reason]++
		if matches >= 1 {
			points >>= matches
			if points == 0 {
				points = 1
			}
		}
		risk.Score += points
		risk.Lanes = appendUnique(risk.Lanes, lane)
		if matches == 0 {
			risk.Reasons = append(risk.Reasons, reason)
			risk.ScoreComponents = append(risk.ScoreComponents, RiskScoreComponent{ID: lane + ":" + reason, Lane: lane, EvidenceKind: "heuristic", Confidence: evidenceHeuristic, Points: points, Matches: 1, TotalMatches: 1, CountedMatches: 1, Reason: reason})
		} else {
			for i := range risk.ScoreComponents {
				if risk.ScoreComponents[i].Reason == reason {
					risk.ScoreComponents[i].Matches++
					risk.ScoreComponents[i].TotalMatches++
					risk.ScoreComponents[i].CountedMatches++
					risk.ScoreComponents[i].Points += points
					break
				}
			}
		}
	}
	addEntrypointRisk(file.Path, lower, add)
	addPathTermRisk(lower, table.terms, add)
	if file.Language == languageGo && risk.FileClass == string(fileclass.Production) {
		addStructuralRisk(snap.Root, file.Path, table, &risk, add)
	}
	patterns := table.patterns
	if file.Language != languageGo {
		patterns = genericRiskPatterns(table.patterns)
	}
	truncated, err := addContentRisk(ctx, snap.Root, file.Path, patterns, func(points int, lane, reason string, line int, text string, counted bool) {
		if !counted {
			for i := range risk.ScoreComponents {
				if risk.ScoreComponents[i].Reason == reason {
					risk.ScoreComponents[i].TotalMatches++
					risk.ScoreComponents[i].Capped = true
					break
				}
			}
			return
		}
		add(points, lane, reason)
		for i := range risk.ScoreComponents {
			if risk.ScoreComponents[i].Reason == reason && len(risk.ScoreComponents[i].Locations) < 3 {
				risk.ScoreComponents[i].EvidenceKind = "source-pattern"
				risk.ScoreComponents[i].Locations = append(risk.ScoreComponents[i].Locations, line)
				risk.ScoreComponents[i].Evidence = append(risk.ScoreComponents[i].Evidence, riskEvidenceText(text))
				break
			}
		}
	})
	if err != nil {
		return FileRisk{}, err
	}
	if truncated {
		risk.Reasons = append(risk.Reasons, "scan:content_truncated")
	}
	risk.Reasons = capReasons(risk.Reasons)
	risk.ReviewPriority = risk.Score
	return risk, nil
}

// addEntrypointRisk scores command entrypoints and main packages.
func addEntrypointRisk(fileRel, lower string, add func(points int, lane, reason string)) {
	if strings.HasPrefix(fileRel, "cmd/") || strings.HasSuffix(lower, "/main.go") || fileRel == "main.go" {
		add(20, "cli-ux", "entrypoint or command surface")
	}
}

// addPathTermRisk scores path-shape terms against the lowercase path.
func addPathTermRisk(lower string, terms []riskPathTerm, add func(points int, lane, reason string)) {
	for _, term := range terms {
		if pathTermMatches(lower, term.Term) {
			add(term.Weight, term.Lane, term.Reason)
		}
	}
}

// addStructuralRisk scores fan-in, fan-out, and symbol-density thresholds,
// recording the observed counts on the risk.
func addStructuralRisk(root, rel string, table riskTable, risk *FileRisk, add func(points int, lane, reason string)) {
	importPath := packageImportPath(root, rel)
	if importedBy := table.importedBy[importPath]; importedBy >= importedByThreshold {
		risk.ImportedBy = importedBy
		add(importedByWeight, "architecture", fmt.Sprintf("package imported by %d files", importedBy))
	}
	if deps := table.dependsOn[rel]; deps >= dependsOnThreshold {
		risk.DependsOn = deps
		add(dependsOnWeight, "coupling", fmt.Sprintf("imports %d dependencies", deps))
	}
	if symbols := table.symbols[rel]; symbols >= symbolsThreshold {
		risk.Symbols = symbols
		add(symbolsWeight, "large-functions", fmt.Sprintf("%d symbols in one file", symbols))
	}
	if signatures := table.signatures[rel]; signatures > 0 {
		add(6*signatures, laneAPIContracts, fmt.Sprintf("%d complex public signature(s)", signatures))
	}
}

// addContentRisk scores bounded content patterns and reports whether the
// read hit the line bound.
func addContentRisk(ctx context.Context, root, rel string, patterns []riskPattern, add func(points int, lane, reason string, line int, text string, counted bool)) (bool, error) {
	lines, truncated, err := readLines(ctx, path.Join(root, filepathFromSlash(rel)))
	if err != nil {
		return false, fmt.Errorf("scan risk source %s: %w", rel, err)
	}
	matches := map[string]int{}
	for _, line := range lines {
		for _, pattern := range patterns {
			if !pattern.Re.MatchString(line.text) || lineExcluded(pattern, line.text) {
				continue
			}
			matches[pattern.ID]++
			add(pattern.Weight, pattern.Lane, pattern.Reason, line.number, line.text, matches[pattern.ID] <= pattern.MaxMatches)
		}
	}
	return truncated, nil
}

// lineExcluded reports whether every Re occurrence on the line is directly
// wrapped in the pattern's bounded Exclude form, so the line carries no
// flaggable occurrence. Partially bounded, multiline, or otherwise
// ambiguous calls keep their match and stay flagged conservatively.
func lineExcluded(pattern riskPattern, text string) bool {
	if pattern.Exclude == nil {
		return false
	}
	matches := pattern.Re.FindAllStringIndex(text, -1)
	for _, match := range matches {
		if !pattern.Exclude.MatchString(text[match[1]:]) {
			return false
		}
	}
	return len(matches) > 0
}

func riskEvidenceText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxRiskEvidenceRunes {
		return text
	}
	return string(runes[:maxRiskEvidenceRunes-3]) + "..."
}

// capReasons bounds the reason list with an explicit sentinel.
func capReasons(reasons []string) []string {
	if len(reasons) <= maxReasons {
		return reasons
	}
	kept := slices.Clone(reasons[:maxReasons])
	return append(kept, fmt.Sprintf("... (%d more signals)", len(reasons)-maxReasons))
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func dedupeAndSort(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	slices.Sort(out)
	return slices.Compact(out)
}

func filepathFromSlash(rel string) string {
	return filepath.FromSlash(rel)
}
