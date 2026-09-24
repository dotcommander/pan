package retrieval

import (
	"maps"
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

// taskCandidates scores every ranked file against the goal. Direct evidence
// comes before structural owners; no positive evidence falls back to rank.
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
// bounding emitted facts independently of the total matched score.
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

// goalTerms lowercases the goal into distinct non-stopword search terms.
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
