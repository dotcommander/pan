package retrieval

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// Task packet bounds. Every cap below emits a truncation record when hit.
const (
	taskDefaultTokens = 4096
	taskTargetLimit   = 6
	taskSymbolCap     = 3
	taskConsumerCap   = 5
	taskTestCap       = 5
	taskImportCap     = 5
	taskRelationCap   = 19
	taskEvidenceCap   = 8
	taskReadNextCap   = 5
	taskSourceLines   = 60
	taskMaxSources    = 3
)

// TaskOptions shapes one task packet build.
type TaskOptions struct {
	// Tokens bounds the encoded packet; <= 0 uses the default.
	Tokens int
	// Consumed are repo-relative paths already in the agent's context; they
	// rank up but their source is omitted from the packet.
	Consumed []string
	// PolicyID records the retrieval policy that produced the ranking. Empty
	// preserves the stable structural/lexical default.
	PolicyID string
}

// TaskEvidence is one matched fact linking the goal to a target file.
type TaskEvidence struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// TaskRelationship is one structural edge from a target to another file.
type TaskRelationship struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Provenance string `json:"provenance"`
}

// TaskSource is one bounded source excerpt embedded for an unread target.
type TaskSource struct {
	Symbol string       `json:"symbol"`
	Lines  []SourceLine `json:"lines"`
}

// TaskTarget is one file selected as relevant to the goal.
type TaskTarget struct {
	Path       string             `json:"path"`
	Package    string             `json:"package,omitempty"`
	Confidence string             `json:"confidence"`
	Symbols    []analyze.Symbol   `json:"symbols,omitempty"`
	Evidence   []TaskEvidence     `json:"evidence,omitempty"`
	Relations  []TaskRelationship `json:"relationships,omitempty"`
	Consumers  []string           `json:"consumers,omitempty"`
	Tests      []string           `json:"tests,omitempty"`
	Imports    []string           `json:"imports,omitempty"`
	Risk       string             `json:"risk"`
	Parse      string             `json:"parse"`
	Source     []TaskSource       `json:"source,omitempty"`
	Consumed   bool               `json:"consumed,omitempty"`
}

// TaskReport is the goal-oriented context packet.
type TaskReport struct {
	Goal             string               `json:"goal"`
	Budget           TaskBudget           `json:"budget"`
	Selection        TaskSelection        `json:"selection"`
	Rules            []string             `json:"rules,omitempty"`
	Targets          []TaskTarget         `json:"targets"`
	ReadNext         []analyze.ReadNext   `json:"read_next,omitempty"`
	VerifyCommands   []string             `json:"verify_commands,omitempty"`
	FollowUpCommands []string             `json:"follow_up_commands,omitempty"`
	Truncations      []analyze.Truncation `json:"truncations,omitempty"`
}

// TaskBudget reports the token bound and the encoded size of the packet.
type TaskBudget struct {
	MaxTokens  int `json:"max_tokens"`
	UsedTokens int `json:"used_tokens"`
}

// TaskSelection reports how targets were chosen.
type TaskSelection struct {
	PolicyID string `json:"policy_id"`
	Strategy string `json:"strategy"`
	Limit    int    `json:"limit"`
	Selected int    `json:"selected"`
}

// Task builds the bounded packet for goal. Structural inputs come from the
// snapshot and its ranking; source excerpts are read from disk. The packet
// is packed greedily: targets are appended in selection order while the
// encoded size fits the token budget, and the first overflow stops packing
// with an explicit truncation record.
func Task(snap analyze.Snapshot, ranked []ranking.RankedFile, goal string, opts TaskOptions) (TaskReport, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return TaskReport{}, errors.New("task goal must not be blank")
	}
	if opts.Tokens < 0 {
		return TaskReport{}, errors.New("task tokens must not be negative")
	}
	if opts.Tokens == 0 {
		opts.Tokens = taskDefaultTokens
	}
	if opts.PolicyID == "" {
		opts.PolicyID = StructuralLexicalPolicy
	}
	consumed, err := NormalizeConsumed(snap.Root, opts.Consumed)
	if err != nil {
		return TaskReport{}, err
	}
	report := TaskReport{
		Goal:   goal,
		Budget: TaskBudget{MaxTokens: opts.Tokens},
		Selection: TaskSelection{
			PolicyID: opts.PolicyID,
			Strategy: "goal evidence, structural owners, rank, path",
			Limit:    taskTargetLimit,
		},
		Rules: snap.Instructions,
	}
	candidates := taskCandidates(ranked, snap, goal)
	sourcesEmbedded := 0
	for _, candidate := range candidates {
		if len(report.Targets) == taskTargetLimit {
			report.addTruncation("targets", len(report.Targets), len(candidates), "target limit")
			break
		}
		target, truncations := buildTaskTarget(snap, ranked, candidate, consumed)
		if target.Consumed || sourcesEmbedded >= taskMaxSources {
			if len(target.Source) > 0 {
				truncations = append(truncations, analyze.Truncation{
					Field:  "targets[" + target.Path + "].source",
					Shown:  0,
					Total:  len(target.Source),
					Reason: "source target cap",
				})
			}
			target.Source = nil
		} else {
			sourcesEmbedded++
		}
		trial := report
		trial.Targets = append(slices.Clone(report.Targets), target)
		trial.Truncations = append(slices.Clone(report.Truncations), truncations...)
		if taskTokens(trial) > opts.Tokens {
			report.addTruncation("targets", len(report.Targets), len(candidates), "token budget")
			break
		}
		report = trial
	}
	report.Selection.Selected = len(report.Targets)
	report.ReadNext = taskReadNext(report.Targets)
	report.VerifyCommands = taskVerifyCommands(report.Targets)
	if len(report.Truncations) > 0 {
		report.FollowUpCommands = []string{
			fmt.Sprintf("pan context map --intent %q for the full ranked map", goal),
		}
	}
	if err := setTaskUsedTokens(&report); err != nil {
		return TaskReport{}, err
	}
	if report.Budget.UsedTokens > opts.Tokens {
		return TaskReport{}, fmt.Errorf("task token budget %d cannot encode report schema", opts.Tokens)
	}
	return report, nil
}

func (r *TaskReport) addTruncation(field string, shown, total int, reason string) {
	if total > shown {
		r.Truncations = append(r.Truncations, analyze.Truncation{Field: field, Shown: shown, Total: total, Reason: reason})
	}
}

// taskTokens estimates the encoded size of the report as ceil(bytes / 4).
func taskTokens(report TaskReport) int {
	data, err := json.Marshal(report)
	if err != nil {
		return 0
	}
	return (len(data) + 3) / 4
}

// setTaskUsedTokens records the encoded report size after its own budget field
// is present. The value can change the JSON size, so converge before enforcing
// the caller's hard budget.
func setTaskUsedTokens(report *TaskReport) error {
	for range 4 {
		used := taskTokens(*report)
		if used == 0 {
			return errors.New("encode task report")
		}
		if report.Budget.UsedTokens == used {
			return nil
		}
		report.Budget.UsedTokens = used
	}
	return errors.New("task report token count did not converge")
}

// taskCandidate is one file with its goal-evidence score and ownership flags.
type taskCandidate struct {
	file          ranking.RankedFile
	evidence      []TaskEvidence
	evidenceTotal int
	relevance     int
	fallback      bool
}

// taskCandidates scores every ranked file against the goal. Field evidence
// (path, package, imports, symbol names, signatures, docs) provides direct
// relevance; importers and lexical callers of positively-matched files are
// credited as structural owners. With no positive evidence the selection
// falls back to ranked non-test order.
func taskCandidates(ranked []ranking.RankedFile, snap analyze.Snapshot, goal string) []taskCandidate {
	terms := goalTerms(goal)
	byPath := make(map[string]*taskCandidate, len(ranked))
	for i := range ranked {
		file := &ranked[i]
		evidence, total, score := fieldEvidence(file, terms)
		if score <= 0 {
			continue
		}
		byPath[file.Path] = &taskCandidate{file: *file, evidence: evidence, evidenceTotal: total, relevance: score}
	}
	// Structural owners: importers and lexical callers of matched files.
	for _, seedPath := range slices.Sorted(maps.Keys(byPath)) {
		seed := byPath[seedPath]
		for _, importer := range impactImporters(seed.file, ranked) {
			addStructuralOwner(byPath, ranked, structuralOwner{target: importer, field: "importer-owner", seed: seedPath, credit: 6})
		}
		for _, callerFile := range lexicalCallerFiles(snap, seed.file) {
			addStructuralOwner(byPath, ranked, structuralOwner{target: callerFile, field: "caller-owner", seed: seedPath, credit: 4})
		}
	}
	var candidates []taskCandidate
	for i := range ranked {
		if candidate, ok := byPath[ranked[i].Path]; ok {
			candidates = append(candidates, *candidate)
		}
	}
	if len(candidates) > 0 {
		slices.SortStableFunc(candidates, orderCandidates)
		return candidates
	}
	for i := range ranked {
		if isTestFile(ranked[i].Path) {
			continue
		}
		candidates = append(candidates, taskCandidate{
			file:     ranked[i],
			evidence: []TaskEvidence{{Field: "fallback", Value: "no positive goal evidence; structural fallback"}},
			fallback: true,
		})
	}
	return candidates
}

func orderCandidates(a, b taskCandidate) int {
	if a.relevance != b.relevance {
		return b.relevance - a.relevance
	}
	if a.file.Score != b.file.Score {
		return b.file.Score - a.file.Score
	}
	return strings.Compare(a.file.Path, b.file.Path)
}

// structuralOwner is one structural-ownership seed: a target file, the
// evidence field and seed value that implicate it, and its relevance
// credit.
type structuralOwner struct {
	target string
	field  string
	seed   string
	credit int
}

func addStructuralOwner(byPath map[string]*taskCandidate, ranked []ranking.RankedFile, owner structuralOwner) {
	if _, exists := byPath[owner.target]; exists {
		return
	}
	if isTestFile(owner.target) {
		return
	}
	for i := range ranked {
		if ranked[i].Path != owner.target {
			continue
		}
		byPath[owner.target] = &taskCandidate{
			file:      ranked[i],
			evidence:  []TaskEvidence{{Field: owner.field, Value: owner.seed}},
			relevance: owner.credit,
		}
		return
	}
}

// lexicalCallerFiles lists files containing call edges into any symbol of
// the target file. Bare-name matching, hence lexical.
func lexicalCallerFiles(snap analyze.Snapshot, target ranking.RankedFile) []string {
	names := make(map[string]struct{}, len(target.Symbols))
	for _, symbol := range target.Symbols {
		names[symbol.Name] = struct{}{}
	}
	seen := make(map[string]struct{})
	var out []string
	for _, edge := range snap.Edges {
		if edge.Kind != edgeKindCalls {
			continue
		}
		if _, callee := names[edge.To]; !callee || edge.Location.Path == target.Path {
			continue
		}
		if _, dup := seen[edge.Location.Path]; dup {
			continue
		}
		seen[edge.Location.Path] = struct{}{}
		out = append(out, edge.Location.Path)
	}
	slices.Sort(out)
	return out
}

// evidenceField couples one matchable surface with its weight.
type evidenceField struct {
	field  string
	value  string
	weight int
}

// fieldEvidence matches goal terms against the file's identity and symbol
// surfaces. Weights mirror how strongly each field implies task ownership.
// Distinct matched facts are returned up to the evidence cap; the total
// count is reported so the cap can be disclosed as a truncation.
func fieldEvidence(file *ranking.RankedFile, terms []string) (evidence []TaskEvidence, total, score int) {
	if len(terms) == 0 {
		return nil, 0, 0
	}
	values := []evidenceField{
		{"path", file.Path, 8},
		{"package", file.Package, 5},
		{"import", strings.Join(file.Imports, " "), 3},
	}
	for _, symbol := range file.Symbols {
		values = append(values,
			evidenceField{"symbol", symbol.Name, 8},
			evidenceField{"signature", symbol.Signature, 4},
			evidenceField{"doc", symbol.Doc, 3},
		)
	}
	seen := make(map[string]struct{})
	for _, value := range values {
		lower := strings.ToLower(value.value)
		for _, term := range terms {
			if !strings.Contains(lower, term) {
				continue
			}
			total++
			key := value.field + "\x00" + value.value
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				if len(evidence) < taskEvidenceCap {
					evidence = append(evidence, TaskEvidence{Field: value.field, Value: value.value})
				}
			}
			score += value.weight
		}
	}
	return evidence, total, score
}

// goalTerms lowercases the goal into match terms: alphanumeric words of two
// or more characters, minus a small stopword set.
func goalTerms(goal string) []string {
	stop := map[string]struct{}{"the": {}, "and": {}, "for": {}, "with": {}, "into": {}, "that": {}, "this": {}, "from": {}, "add": {}, "fix": {}}
	seen := make(map[string]struct{})
	var terms []string
	for _, field := range strings.FieldsFunc(strings.ToLower(goal), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(field) < 2 {
			continue
		}
		if _, drop := stop[field]; drop {
			continue
		}
		if _, dup := seen[field]; dup {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}

// buildTaskTarget assembles one target's bounded evidence and relationships.
// Every applied cap appends a truncation record.
func buildTaskTarget(snap analyze.Snapshot, ranked []ranking.RankedFile, candidate taskCandidate, consumed []string) (TaskTarget, []analyze.Truncation) {
	file := candidate.file
	var truncations []analyze.Truncation
	symbols := slices.Clone(file.Symbols)
	slices.SortStableFunc(symbols, func(a, b analyze.Symbol) int {
		if a.Exported != b.Exported {
			if a.Exported {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(symbols) > taskSymbolCap {
		truncations = append(truncations, analyze.Truncation{Field: "targets[" + file.Path + "].symbols", Shown: taskSymbolCap, Total: len(symbols), Reason: "symbol cap"})
		symbols = symbols[:taskSymbolCap]
	}

	importers := impactImporters(file, ranked)
	tests := impactTests(file.Path, ranked)
	var relations []TaskRelationship
	for _, importer := range importers {
		relations = append(relations, TaskRelationship{Kind: "consumer", Path: importer, Provenance: analyze.ConfidenceSyntactic})
	}
	for _, test := range tests {
		relations = append(relations, TaskRelationship{Kind: "test", Path: test, Provenance: analyze.ConfidenceHeuristic})
	}
	for _, caller := range lexicalCallerFiles(snap, file) {
		relations = append(relations, TaskRelationship{Kind: "caller", Path: caller, Provenance: analyze.ConfidenceLexical})
	}
	slices.SortStableFunc(relations, func(a, b TaskRelationship) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Path, b.Path)
	})

	consumers := capTaskStrings(importers, taskConsumerCap, "targets["+file.Path+"].consumers", &truncations)
	limitedTests := capTaskStrings(tests, taskTestCap, "targets["+file.Path+"].tests", &truncations)
	imports := capTaskStrings(file.Imports, taskImportCap, "targets["+file.Path+"].imports", &truncations)
	if len(relations) > taskRelationCap {
		truncations = append(truncations, analyze.Truncation{Field: "targets[" + file.Path + "].relationships", Shown: taskRelationCap, Total: len(relations), Reason: "relationship cap"})
		relations = relations[:taskRelationCap]
	}
	if candidate.evidenceTotal > len(candidate.evidence) {
		truncations = append(truncations, analyze.Truncation{Field: "targets[" + file.Path + "].evidence", Shown: len(candidate.evidence), Total: candidate.evidenceTotal, Reason: "evidence cap"})
	}

	risk := impactRiskLevel(file, importers, tests)
	target := TaskTarget{
		Path:       file.Path,
		Package:    file.Package,
		Confidence: taskConfidence(candidate),
		Symbols:    symbols,
		Evidence:   candidate.evidence,
		Relations:  relations,
		Consumers:  consumers,
		Tests:      limitedTests,
		Imports:    imports,
		Risk:       risk,
		Parse:      parseMethod(file),
		Consumed:   slices.Contains(consumed, file.Path),
	}
	if !target.Consumed {
		target.Source = taskSource(snap, file.Path, symbols, &truncations)
	}
	return target, truncations
}

func taskConfidence(candidate taskCandidate) string {
	switch {
	case candidate.fallback:
		return "fallback"
	case candidate.relevance >= 16:
		return "high"
	case candidate.relevance >= 8:
		return "medium"
	default:
		return "low"
	}
}

func capTaskStrings(values []string, cap int, field string, truncations *[]analyze.Truncation) []string {
	values = sortedUniqueStrings(values)
	if len(values) > cap {
		*truncations = append(*truncations, analyze.Truncation{Field: field, Shown: cap, Total: len(values), Reason: "relationship cap"})
		return values[:cap]
	}
	return values
}

// taskSource embeds the first symbol's bounded source excerpt.
func taskSource(snap analyze.Snapshot, relPath string, symbols []analyze.Symbol, truncations *[]analyze.Truncation) []TaskSource {
	source, ok := snap.Source(relPath)
	if !ok {
		return nil
	}
	var out []TaskSource
	for _, symbol := range symbols {
		if symbol.Location.Line <= 0 {
			continue
		}
		lines, truncated, _ := readSymbolSource(source, symbol, taskSourceLines)
		if len(lines) == 0 {
			continue
		}
		if truncated {
			span := symbolEnd(symbol) - symbol.Location.Line + 1
			*truncations = append(*truncations, analyze.Truncation{
				Field:  "targets[" + relPath + "].source[" + symbol.Name + "]",
				Shown:  len(lines),
				Total:  span,
				Reason: "60 lines per symbol cap",
			})
		}
		out = append(out, TaskSource{Symbol: symbol.Name, Lines: lines})
		break
	}
	return out
}

// taskReadNext derives bounded inspection spans from the selected targets.
func taskReadNext(targets []TaskTarget) []analyze.ReadNext {
	var items []analyze.ReadNext
	for _, target := range targets {
		for _, symbol := range target.Symbols {
			if symbol.Location.Line <= 0 {
				continue
			}
			items = append(items, readNextSpan(target.Path, symbol.Location.Line, symbolEnd(symbol), "inspect target symbol "+symbol.Name))
			break
		}
		for _, test := range target.Tests {
			items = append(items, readNextSpan(test, 1, 1, "inspect likely test coverage for "+target.Path))
		}
		if len(items) >= taskReadNextCap {
			break
		}
	}
	return dedupeReadNext(items, taskReadNextCap)
}

// taskVerifyCommands suggests bounded verification: one go test invocation
// per distinct affected test directory.
func taskVerifyCommands(targets []TaskTarget) []string {
	dirs := make(map[string]struct{})
	for _, target := range targets {
		for _, test := range target.Tests {
			if !strings.HasSuffix(test, ".go") {
				continue
			}
			dir := path.Dir(test)
			if dir == "." {
				dirs["./"] = struct{}{}
				continue
			}
			dirs["./"+dir] = struct{}{}
		}
	}
	out := make([]string, 0, len(dirs))
	for dir := range dirs {
		out = append(out, "go test "+dir)
	}
	slices.Sort(out)
	return out
}

// NormalizeConsumed canonicalizes consumed paths against root: relative
// slash paths, deduplicated and sorted, rejecting paths outside root.
func NormalizeConsumed(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	var out []string
	for _, raw := range paths {
		if strings.TrimSpace(raw) == "" {
			return nil, errors.New("consumed path must not be blank")
		}
		abs := raw
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, filepath.FromSlash(raw))
		}
		rel, err := filepath.Rel(root, filepath.Clean(abs))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("consumed path %q is outside the repository root", raw)
		}
		rel = filepath.ToSlash(rel)
		if _, dup := seen[rel]; !dup {
			seen[rel] = struct{}{}
			out = append(out, rel)
		}
	}
	slices.Sort(out)
	return out, nil
}
