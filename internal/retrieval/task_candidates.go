package retrieval

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// taskCandidate is one file with its goal-evidence score and ownership flags.
type taskCandidate struct {
	file          ranking.RankedFile
	evidence      []TaskEvidence
	evidenceTotal int
	relevance     int
	fallback      bool
}

// symbolGoalRE identifies bare-symbol goals: namespace-qualified names,
// underscore-prefixed identifiers, mixed-case identifiers, or leading
// capitals. Plain lowercase phrases are natural-language goals.
var symbolGoalRE = regexp.MustCompile(`^(?:` +
	`[A-Za-z_][A-Za-z0-9_]*(?:(?:::|\\|->|\.)[A-Za-z_][A-Za-z0-9_]*)+` + // namespace-qualified
	`|_[A-Za-z0-9_]*` + // leading underscore
	`|[A-Za-z][A-Za-z0-9]*[A-Z_][A-Za-z0-9_]*` + // contains uppercase or underscore
	`|[A-Z][A-Za-z0-9]*` + // starts with uppercase
	`)$`)

// isSymbolGoal reports whether the goal is a bare symbol lookup rather than
// a natural-language request.
func isSymbolGoal(goal string) bool {
	return symbolGoalRE.MatchString(strings.TrimSpace(goal))
}

// taskCandidates scores every ranked file against the goal. Direct evidence
// comes before structural owners; no positive evidence falls back to rank.
func taskCandidates(ranked []ranking.RankedFile, snap analyze.Snapshot, goal string) []taskCandidate {
	terms := goalTerms(goal)
	symbolMode := isSymbolGoal(goal)
	byPath := make(map[string]*taskCandidate, len(ranked))
	for i := range ranked {
		file := &ranked[i]
		evidence, total, score := fieldEvidence(file, terms, symbolMode)
		if score <= 0 {
			continue
		}
		byPath[file.Path] = &taskCandidate{file: *file, evidence: evidence, evidenceTotal: total, relevance: score}
	}
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
	seenFallback := make(map[string]bool)
	for i := range ranked {
		if isTestFile(ranked[i].Path) || seenFallback[ranked[i].Path] {
			continue
		}
		seenFallback[ranked[i].Path] = true
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

type evidenceField struct {
	field  string
	value  string
	weight int
}

// fieldEvidence ranks path, package, imports, and symbol surfaces while
// bounding emitted facts independently of the total matched score. Exact
// term-part matches score full weight; prefix-overlap matches score half.
// Symbol goals raise exact-name evidence (symbols, signatures) and lower
// path evidence, leaning toward definers over files whose path merely
// echoes the term.
func fieldEvidence(file *ranking.RankedFile, terms []string, symbolMode bool) (evidence []TaskEvidence, total, score int) {
	if len(terms) == 0 {
		return nil, 0, 0
	}
	pathWeight, symbolWeight, signatureWeight := 8, 8, 4
	if symbolMode {
		pathWeight, symbolWeight, signatureWeight = 4, 12, 6
	}
	values := []evidenceField{
		{"path", file.Path, pathWeight},
		{"package", file.Package, 5},
		{"import", strings.Join(file.Imports, " "), 3},
	}
	for _, symbol := range file.Symbols {
		values = append(values,
			evidenceField{"symbol", symbol.Name, symbolWeight},
			evidenceField{"signature", symbol.Signature, signatureWeight},
			evidenceField{"doc", symbol.Doc, 3},
		)
	}
	seen := make(map[string]struct{})
	for _, value := range values {
		parts := evidenceParts(value.field, value.value)
		for _, term := range terms {
			matched, exact := ranking.MatchPart(parts, term)
			if !matched {
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
			if exact {
				score += value.weight
			} else {
				score += max(1, value.weight/2)
			}
		}
	}
	return evidence, total, score
}

// evidenceParts returns the term-matching vocabulary of one evidence field.
// Paths and packages use path-segment sub-tokens; symbol names use
// identifier sub-tokens; signatures, docs, and import lists are free text.
func evidenceParts(field, value string) []string {
	if field == "path" || field == "package" {
		return ranking.PathTerms(value)
	}
	if field == "symbol" {
		return ranking.SplitIdentifier(value)
	}
	var parts []string
	split := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	for _, token := range strings.FieldsFunc(strings.ToLower(value), split) {
		parts = append(parts, ranking.SplitIdentifier(token)...)
	}
	return parts
}

// goalTerms lowercases the goal into distinct non-stopword search terms,
// expanded with identifier sub-tokens so compound goals match compound names.
func goalTerms(goal string) []string {
	return ranking.ExpandTerms(goal, 2, ranking.CommonStopwords)
}
