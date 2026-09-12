package scan

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// Effect kind and review-lane vocabulary shared by the effects, risk, and
// inventory packets. Constants keep the recurring names single-sourced.
const (
	kindFilesystemWrite   = "filesystem-write"
	kindFilesystemRead    = "filesystem-read"
	kindSubprocess        = "subprocess"
	kindProcessExit       = "process-exit"
	kindHTTP              = "http"
	kindDatabase          = "database"
	kindSerialization     = "serialization"
	kindSecret            = "secret"
	kindCrypto            = "crypto"
	kindTime              = "time"
	kindRandomness        = "randomness"
	kindContextBackground = "context-background"
	kindGoroutine         = "goroutine"
	kindUnboundedRead     = "unbounded-read"

	laneSecurity             = "security"
	laneAPIContracts         = "api-contracts"
	laneDataIntegrity        = "data-integrity"
	laneErrorHandling        = "error-handling"
	laneLifecycleConcurrency = "lifecycle-concurrency"
	lanePerformance          = "performance"
	laneBestPractices        = "best-practices"

	evidenceHeuristic  = "heuristic"
	confidenceMedium   = "medium"
	boundaryFilesystem = "Filesystem"
	evidenceParsedCall = "parsed call expression"
)

// Shared detection vocabulary used across the scan packets: the source
// language tag snapshots stamp on Go files and the path-term spelling the
// risk and inventory tables share.
const (
	languageGo      = "go"
	languagePython  = "python"
	languageUnknown = "unknown"
	termCredential  = "credential"
)

// Effect is one static side-effect or trust-boundary lead.
type Effect struct {
	Kind       string `json:"kind"`
	Op         string `json:"op,omitempty"`
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line"`
	Lane       string `json:"lane,omitempty"`
	Evidence   string `json:"evidence"`
	Provenance string `json:"provenance,omitempty"`
}

// EffectFile groups effect leads by source file.
type EffectFile struct {
	ID            string   `json:"id,omitempty"`
	Path          string   `json:"path"`
	Score         int      `json:"score,omitempty"`
	EvidenceClass string   `json:"evidence_class,omitempty"`
	Confidence    string   `json:"confidence,omitempty"`
	Lanes         []string `json:"lanes"`
	Effects       []Effect `json:"effects"`
	OmittedReason string   `json:"omitted_reason,omitempty"`
}

// EffectKind groups files sharing one side-effect kind.
type EffectKind struct {
	ID            string   `json:"id,omitempty"`
	Name          string   `json:"name"`
	Reason        string   `json:"reason"`
	Lane          string   `json:"lane"`
	Files         []string `json:"files"`
	Command       string   `json:"command,omitempty"`
	OmittedReason string   `json:"omitted_reason,omitempty"`
}

// EffectsReport is the deterministic side-effect and trust-boundary packet.
type EffectsReport struct {
	Files              []EffectFile `json:"files"`
	FilesOmittedReason string       `json:"files_omitted_reason,omitempty"`
	Kinds              []EffectKind `json:"kinds"`
	Truncations        []Truncation `json:"truncations,omitempty"`
}

// EffectsOptions selects the bounded effects packet. Limit applies after a
// kind filter, so a narrow request is never hidden by unrelated file hits.
type EffectsOptions struct {
	Limit    int
	Kind     string
	Language string
}

// Validate rejects invalid packet selectors before source files are read.
func (o EffectsOptions) Validate() error {
	if o.Limit < 0 {
		return errors.New("effects limit must not be negative")
	}
	if o.Kind != "" && !slices.Contains(EffectKinds(), o.Kind) {
		return fmt.Errorf("unknown effect kind %q", o.Kind)
	}
	if o.Language != "" && !slices.Contains(effectLanguages(), o.Language) {
		return fmt.Errorf("unsupported effects language %q; supported languages: %s", o.Language, strings.Join(effectLanguages(), ", "))
	}
	return nil
}

// effectPattern is one bounded line pattern classifying a side effect.
type effectPattern struct {
	Kind string
	Lane string
	Re   *regexp.Regexp
}

// effectTable returns the bounded line patterns classifying side effects.
// It is built per packet run and threaded through the helpers that need it,
// so the package owns no mutable global tables.
func effectTable() []effectPattern {
	return []effectPattern{
		{Kind: kindFilesystemWrite, Lane: laneDataIntegrity, Re: regexp.MustCompile(`\b(?:os\.)?(?:WriteFile|OpenFile|Create(?:Temp)?|MkdirAll|Rename|Remove(?:All)?)\s*\(`)},
		{Kind: kindFilesystemRead, Lane: laneDataIntegrity, Re: regexp.MustCompile(`\b(?:os\.)?(?:ReadFile|Open)\s*\(`)},
		{Kind: kindSubprocess, Lane: laneErrorHandling, Re: regexp.MustCompile(`\bexec\.Command(?:Context)?\(`)},
		{Kind: kindProcessExit, Lane: laneErrorHandling, Re: regexp.MustCompile(`\b(?:os\.Exit|log\.Fatal|panic)\s*\(`)},
		{Kind: kindHTTP, Lane: laneAPIContracts, Re: regexp.MustCompile(`\b(?:http\.(?:Get|Post|Head|NewRequest|NewRequestWithContext|Handle|HandleFunc|ListenAndServe)|fetch|axios)\s*\(|\.Do\(`)},
		{Kind: kindDatabase, Lane: laneDataIntegrity, Re: regexp.MustCompile(`\b(?:sql|pgx|pgxpool)\.(?:Open|New|Connect|ConnectConfig|Ping|PingContext|Query|QueryContext|QueryRow|QueryRowContext|Exec|ExecContext|Begin|BeginTx|Commit|Rollback|Prepare|PrepareContext|SendBatch|CopyFrom)\(`)},
		{Kind: kindSerialization, Lane: laneAPIContracts, Re: regexp.MustCompile(`\b(?:json|yaml)\.(?:Marshal|Unmarshal|NewEncoder|NewDecoder)\(`)},
		{Kind: kindSecret, Lane: laneSecurity, Re: regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|token)\s*[:=]`)},
		{Kind: kindCrypto, Lane: laneSecurity, Re: regexp.MustCompile(`\bcrypto/[a-z0-9]+`)},
		{Kind: kindTime, Lane: laneDataIntegrity, Re: regexp.MustCompile(`\btime\.(?:Now|After|NewTicker)\(`)},
		{Kind: kindRandomness, Lane: laneSecurity, Re: regexp.MustCompile(`\bmath/rand\b|\brand\.(?:Intn|Float64|Read|Seed)\(`)},
		{Kind: kindContextBackground, Lane: laneLifecycleConcurrency, Re: regexp.MustCompile(`\bcontext\.Background\(\)`)},
		{Kind: kindGoroutine, Lane: laneLifecycleConcurrency, Re: regexp.MustCompile(`\bgo\s+func\(`)},
		{Kind: kindUnboundedRead, Lane: lanePerformance, Re: regexp.MustCompile(`\bio\.ReadAll\(`)},
	}
}

func effectLanguages() []string {
	return []string{"c", "cpp", languageGo, "java", "javascript", "jsx", "php", languagePython, "ruby", "rust", "tsx", "typescript"}
}

// Effects extracts side-effect and trust-boundary leads from non-test Go
// files in the snapshot. top > 0 caps the returned file list; the kinds
// summary always reflects every discovered effect.
func Effects(ctx context.Context, snap analyze.Snapshot, top int) (EffectsReport, error) {
	return EffectsWithOptions(ctx, snap, EffectsOptions{Limit: top})
}

// EffectsWithOptions extracts side-effect and trust-boundary leads from
// non-test Go files in the snapshot. Kind filtering happens before the
// per-file hit cap and file limit, preserving matching evidence.
func EffectsWithOptions(ctx context.Context, snap analyze.Snapshot, options EffectsOptions) (EffectsReport, error) {
	if err := options.Validate(); err != nil {
		return EffectsReport{}, err
	}
	patterns := effectTable()
	var files []EffectFile
	kindFiles := map[string][]string{}
	var truncations []Truncation
	for _, file := range snap.Files {
		scanned, err := scanEffectsFile(ctx, snap.Root, file, options, patterns)
		if err != nil {
			return EffectsReport{}, err
		}
		if !scanned.ok {
			continue
		}
		files = append(files, scanned.file)
		truncations = append(truncations, scanned.truncations...)
		for _, effect := range scanned.effects {
			kindFiles[effect.Kind] = append(kindFiles[effect.Kind], file.Path)
		}
	}
	slices.SortFunc(files, func(a, b EffectFile) int {
		if len(a.Effects) != len(b.Effects) {
			return len(b.Effects) - len(a.Effects)
		}
		return strings.Compare(a.Path, b.Path)
	})
	total := len(files)
	if options.Limit > 0 && len(files) > options.Limit {
		files = files[:options.Limit]
	}
	report := EffectsReport{Files: files, Kinds: buildEffectKinds(kindFiles, patterns), Truncations: truncations}
	switch {
	case len(files) == 0:
		report.FilesOmittedReason = "no side-effect data extracted from scanned files"
	case len(files) < total:
		report.FilesOmittedReason = fmt.Sprintf("showing %d of %d files; truncated by --limit", len(files), total)
	}
	return report, nil
}

type scannedEffectsFile struct {
	file        EffectFile
	effects     []Effect
	truncations []Truncation
	ok          bool
}

func scanEffectsFile(ctx context.Context, root string, file analyze.File, options EffectsOptions, patterns []effectPattern) (scannedEffectsFile, error) {
	if file.Language == languageUnknown || isTestPath(file.Path) || (options.Language != "" && file.Language != options.Language) {
		return scannedEffectsFile{}, nil
	}
	lines, truncated, err := readLines(ctx, path.Join(root, filepathFromSlash(file.Path)))
	if err != nil {
		return scannedEffectsFile{}, fmt.Errorf("scan effects source %s: %w", file.Path, err)
	}
	effects := scanEffects(file.Language, lines, patterns)
	if options.Kind != "" {
		effects = filterEffectsByKind(effects, options.Kind)
	}
	if len(effects) == 0 {
		return scannedEffectsFile{}, nil
	}
	for index := range effects {
		effects[index].Path = file.Path
		if effects[index].Lane == "" {
			effects[index].Lane = effectKindLane(effects[index].Kind, patterns)
		}
	}
	effects = normalizeEffects(effects)
	lanes := effectLanes(effects, patterns)
	effectFile := EffectFile{ID: "pan:effect:" + effectSlug(file.Path), Path: file.Path, Score: effectScore(effects), EvidenceClass: effectEvidenceClass(lanes), Confidence: effectConfidence(lanes), Lanes: lanes, Effects: effects}
	var truncations []Truncation
	if len(effects) > perFileHitCap {
		effectFile.Effects = effects[:perFileHitCap:perFileHitCap]
		truncations = append(truncations, Truncation{Field: "files[" + file.Path + "].effects", Shown: perFileHitCap, Total: len(effects), Reason: "effects per-file cap"})
	}
	if truncated {
		truncations = append(truncations, Truncation{Field: "files[" + file.Path + "].lines", Shown: len(lines), Total: len(lines) + 1, Reason: "line bound reached; tail not inspected"})
	}
	return scannedEffectsFile{file: effectFile, effects: effects, truncations: truncations, ok: true}, nil
}

func filterEffectsByKind(effects []Effect, kind string) []Effect {
	filtered := make([]Effect, 0, len(effects))
	for _, effect := range effects {
		if effect.Kind == kind {
			filtered = append(filtered, effect)
		}
	}
	return filtered
}

// EffectPaths returns the sorted, unique paths represented by the report.
func (r EffectsReport) EffectPaths() []string {
	paths := make([]string, 0, len(r.Files))
	for _, file := range r.Files {
		paths = append(paths, file.Path)
	}
	return dedupeAndSort(paths)
}

func scanEffectLines(lines []sourceLine, patterns []effectPattern) []Effect {
	var effects []Effect
	for _, line := range lines {
		for _, pattern := range patterns {
			if pattern.Re.MatchString(line.text) {
				effects = append(effects, Effect{Kind: pattern.Kind, Op: effectOperation(line.text), Line: line.number, Lane: pattern.Lane, Evidence: evidence(line.text), Provenance: "heuristic_code_span"})
			}
		}
	}
	return effects
}

// scanEffects uses parser-confirmed call-expression spans. If a parser rejects
// the bounded source span, the source packet retains its established heuristic
// fallback instead of dropping an otherwise useful lead.
func scanEffects(language string, lines []sourceLine, patterns []effectPattern) []Effect {
	if language == languageGo {
		if effects, err := scanGoEffects(lines, patterns); err == nil {
			return effects
		}
	}
	source := make([]string, 0, len(lines))
	for _, line := range lines {
		source = append(source, line.text)
	}
	calls, err := analyze.TreeSitterCalls([]byte(strings.Join(source, "\n")), language)
	if err != nil {
		return scanEffectLines(lines, patterns)
	}
	var effects []Effect
	for _, call := range calls {
		for _, pattern := range patterns {
			if pattern.Re.MatchString(call.Text) {
				effects = append(effects, Effect{Kind: pattern.Kind, Op: effectOperation(call.Text), Line: call.Line, Lane: pattern.Lane, Evidence: evidenceParsedCall, Provenance: "tree_sitter_call"})
			}
		}
	}
	// These syntactic forms are not calls. Keep their explicit source-shape
	// checks alongside parser-confirmed calls.
	for _, effect := range scanEffectLines(lines, patterns) {
		if effect.Kind == kindSecret || effect.Kind == kindCrypto || effect.Kind == kindGoroutine {
			effects = append(effects, effect)
		}
	}
	return normalizeEffects(effects)
}

func effectLanes(effects []Effect, patterns []effectPattern) []string {
	seen := map[string]bool{}
	var out []string
	for _, effect := range effects {
		lane := effectKindLane(effect.Kind, patterns)
		if seen[lane] {
			continue
		}
		seen[lane] = true
		out = append(out, lane)
	}
	slices.Sort(out)
	return out
}

func buildEffectKinds(kindFiles map[string][]string, patterns []effectPattern) []EffectKind {
	names := make([]string, 0, len(kindFiles))
	for name := range kindFiles {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]EffectKind, 0, len(names))
	for _, name := range names {
		out = append(out, EffectKind{
			ID:      "pan:effect-kind:" + effectSlug(name),
			Name:    name,
			Reason:  effectKindReason(name),
			Lane:    effectKindLane(name, patterns),
			Files:   dedupeAndSort(kindFiles[name]),
			Command: "pan scan effects --format json",
		})
	}
	return out
}

func effectKindReason(name string) string {
	switch name {
	case kindFilesystemWrite:
		return "writes, renames, or deletes can affect data integrity and rollback behavior"
	case kindFilesystemRead:
		return "file reads can affect config, import, and input validation behavior"
	case kindSubprocess:
		return "subprocess boundaries need timeout, stderr, and exit-code handling"
	case kindProcessExit:
		return "process termination paths affect cleanup and user-facing errors"
	case kindHTTP:
		return "HTTP boundaries need request, response, timeout, and contract checks"
	case kindDatabase:
		return "database open, query, and transaction calls need transaction, migration, and error-path checks"
	case kindSerialization:
		return "serialization boundaries define API, file, and persistence contracts"
	case kindSecret:
		return "secret-like assignments need storage, logging, and config review"
	case kindCrypto:
		return "crypto boundaries need algorithm and key-handling review"
	case kindTime:
		return "time-dependent logic can affect ordering, expiry, and reproducibility"
	case kindRandomness:
		return "randomness can affect security, determinism, and reproducibility"
	case kindContextBackground:
		return "context.Background in source can break caller cancellation chains"
	case kindGoroutine:
		return "goroutine launches need exit and ownership checks"
	case kindUnboundedRead:
		return "io.ReadAll without an obvious local cap needs resource-bound review"
	default:
		return "static side-effect signal"
	}
}

func effectKindLane(name string, patterns []effectPattern) string {
	for _, pattern := range patterns {
		if pattern.Kind == name {
			return pattern.Lane
		}
	}
	return laneBestPractices
}

func normalizeEffects(effects []Effect) []Effect {
	seen := make(map[string]bool, len(effects))
	out := effects[:0]
	for _, effect := range effects {
		key := effect.Kind + "\x00" + effect.Op + "\x00" + strconv.Itoa(effect.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, effect)
	}
	slices.SortFunc(out, func(a, b Effect) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Op, b.Op)
	})
	return out
}

func effectOperation(text string) string {
	if index := strings.IndexByte(text, '('); index > 0 {
		return strings.TrimSpace(text[:index])
	}
	return ""
}

func effectSlug(value string) string {
	value = strings.NewReplacer("/", "-", "\\", "-", ".", "-").Replace(value)
	return strings.Trim(value, "-")
}

func effectScore(effects []Effect) int {
	score := 0
	for _, effect := range effects {
		switch effect.Kind {
		case kindFilesystemWrite, kindSubprocess, kindDatabase:
			score += 10
		case kindSecret, kindUnboundedRead:
			score += 9
		case kindProcessExit, kindHTTP, kindCrypto, kindGoroutine:
			score += 8
		case kindContextBackground:
			score += 7
		case kindSerialization, kindRandomness:
			score += 5
		case kindFilesystemRead:
			score += 4
		case kindTime:
			score += 3
		}
	}
	return score
}

func effectEvidenceClass(lanes []string) string {
	for _, lane := range lanes {
		if lane == laneAPIContracts {
			return "ast"
		}
	}
	return evidenceHeuristic
}

func effectConfidence(lanes []string) string {
	if effectEvidenceClass(lanes) == "ast" {
		return "high"
	}
	return confidenceMedium
}

// EffectBoundaries returns stable impact boundary labels evidenced for path.
func EffectBoundaries(report EffectsReport, path string) []string {
	seen := map[string]bool{}
	for _, file := range report.Files {
		if file.Path != path {
			continue
		}
		for _, effect := range file.Effects {
			name := effectBoundary(effect.Kind)
			if name != "" {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func effectBoundary(kind string) string {
	switch kind {
	case kindFilesystemWrite, kindFilesystemRead:
		return boundaryFilesystem
	case kindHTTP:
		return "HTTP"
	case kindDatabase:
		return "Database"
	case kindSubprocess:
		return "Subprocess"
	case kindProcessExit:
		return "Exit"
	case kindSecret:
		return "Secret"
	case kindCrypto:
		return "Crypto"
	case kindSerialization:
		return "Serialization"
	case kindTime:
		return "Time"
	case kindRandomness:
		return "Randomness"
	case kindGoroutine, kindContextBackground:
		return "Concurrency"
	case kindUnboundedRead:
		return "UnboundedRead"
	default:
		return ""
	}
}

// ApplyEffectScores adds the ranking contribution used by the Pan audit
// packet. Callers pass the score keyed by repository-relative file path.
func ApplyEffectScores(report *EffectsReport, scores map[string]int) {
	for index := range report.Files {
		report.Files[index].Score += min(scores[report.Files[index].Path]/20, 10)
	}
}
