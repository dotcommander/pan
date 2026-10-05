package bench

import (
	"cmp"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// BM25 parameters (standard Robertson/Sparck-Jones defaults) and the ranked
// list length the baseline contributes to report rows.
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// tokenPattern splits text into lowercase word tokens.
var tokenPattern = regexp.MustCompile(`[a-z0-9_]+`)

// tokenize lowercases text and returns its word tokens.
func tokenize(text string) []string {
	return tokenPattern.FindAllString(strings.ToLower(text), -1)
}

// bm25Document is one scored file: its token frequencies and length.
type bm25Document struct {
	path      string
	termCount map[string]int
	length    int
}

// BM25Rank scores every snapshot file against the query with BM25 over
// content tokens plus path-segment tokens, returning paths ordered by
// descending score with the path as the deterministic tie-break. Files whose
// captured source is unavailable are skipped. topK <= 0 returns an empty
// ranking.
func BM25Rank(snap analyze.Snapshot, query string, topK int) []string {
	if topK <= 0 {
		return nil
	}
	documents := make([]bm25Document, 0, len(snap.Files))
	for _, file := range snap.Files {
		source, ok := snap.Source(file.Path)
		if !ok {
			continue
		}
		tokens := tokenize(string(source))
		tokens = append(tokens, tokenize(strings.ReplaceAll(file.Path, "/", " "))...)
		document := bm25Document{path: file.Path, termCount: make(map[string]int, len(tokens)), length: len(tokens)}
		for _, token := range tokens {
			document.termCount[token]++
		}
		documents = append(documents, document)
	}
	if len(documents) == 0 {
		return nil
	}
	averageLength := 0.0
	for _, document := range documents {
		averageLength += float64(document.length)
	}
	averageLength /= float64(len(documents))
	documentFrequency := make(map[string]int)
	for _, document := range documents {
		for term := range document.termCount {
			documentFrequency[term]++
		}
	}
	total := float64(len(documents))
	type scored struct {
		path  string
		score float64
	}
	queryTerms := tokenize(query)
	scores := make([]scored, 0, len(documents))
	for _, document := range documents {
		var sum float64
		for _, term := range queryTerms {
			frequency := document.termCount[term]
			if frequency == 0 {
				continue
			}
			df := float64(documentFrequency[term])
			idf := math.Log(1 + (total-df+0.5)/(df+0.5))
			sum += idf * (float64(frequency) * (bm25K1 + 1)) /
				(float64(frequency) + bm25K1*(1-bm25B+bm25B*float64(document.length)/averageLength))
		}
		scores = append(scores, scored{path: document.path, score: sum})
	}
	slices.SortFunc(scores, func(a, b scored) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		return strings.Compare(a.path, b.path)
	})
	if len(scores) > topK {
		scores = scores[:topK]
	}
	out := make([]string, 0, len(scores))
	for _, entry := range scores {
		out = append(out, entry.path)
	}
	return out
}
