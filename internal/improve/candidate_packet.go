package improve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
)

const (
	maxCandidateFiles      = 12
	candidateNoGoFiles     = "no_go_files_found"
	candidateFunction      = "function"
	candidateMethod        = "method"
	candidateActionInspect = "inspect"
)

// BuildCandidatePacket produces a bounded, deterministic read-only shortlist
// for a refactor proposal. It uses Pan's parsed repository snapshot for file
// and declaration facts, then records lexical reference evidence as a lead
// rather than a deletion verdict.
func BuildCandidatePacket(ctx context.Context, root string, exclude []string) (CandidatePacket, error) {
	cfg := config.Default()
	cfg.Exclude = append(cfg.Exclude, exclude...)
	snapshot, err := analyze.Build(ctx, root, cfg)
	if err != nil {
		return CandidatePacket{}, fmt.Errorf("analyze candidates: %w", err)
	}
	if len(snapshot.Files) == 0 {
		return CandidatePacket{SkippedSignals: []string{candidateNoGoFiles}}, nil
	}

	corpora, err := candidateCorpora(ctx, snapshot)
	if err != nil {
		return CandidatePacket{}, err
	}
	if corpora.source == "" && corpora.tests == "" {
		return CandidatePacket{SkippedSignals: []string{candidateNoGoFiles}}, nil
	}
	candidates, err := candidateSymbols(ctx, snapshot, corpora)
	if err != nil {
		return CandidatePacket{}, err
	}
	return candidatePacket(candidates), nil
}

type packetCandidate struct {
	path      string
	name      string
	line      int
	refs      int
	testRefs  int
	score     int
	bodyLines int
	snippet   string
	exported  bool
	method    bool
	kind      string
}

type candidateCorpus struct {
	source string
	tests  string
}

func candidateCorpora(ctx context.Context, snapshot analyze.Snapshot) (candidateCorpus, error) {
	var source, tests strings.Builder
	for _, file := range snapshot.Files {
		if err := ctx.Err(); err != nil {
			return candidateCorpus{}, err
		}
		if file.Language != "go" {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(snapshot.Root, filepath.FromSlash(file.Path)))
		if err != nil {
			return candidateCorpus{}, fmt.Errorf("read candidate source %s: %w", file.Path, err)
		}
		if strings.HasSuffix(file.Path, "_test.go") {
			tests.Write(contents)
		} else {
			source.Write(contents)
		}
		if strings.HasSuffix(file.Path, "_test.go") {
			tests.WriteByte('\n')
		} else {
			source.WriteByte('\n')
		}
	}
	return candidateCorpus{source: source.String(), tests: tests.String()}, nil
}

func candidateSymbols(ctx context.Context, snapshot analyze.Snapshot, corpora candidateCorpus) ([]packetCandidate, error) {
	contents, err := candidateFileContents(snapshot)
	if err != nil {
		return nil, err
	}
	return rankCandidateSymbols(ctx, snapshot, contents, corpora)
}

func candidateFileContents(snapshot analyze.Snapshot) (map[string]string, error) {
	contents := make(map[string]string, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if file.Language != "go" || strings.HasSuffix(file.Path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(snapshot.Root, filepath.FromSlash(file.Path)))
		if err != nil {
			return nil, fmt.Errorf("read candidate source %s: %w", file.Path, err)
		}
		contents[file.Path] = string(data)
	}
	return contents, nil
}

func rankCandidateSymbols(ctx context.Context, snapshot analyze.Snapshot, contents map[string]string, corpora candidateCorpus) ([]packetCandidate, error) {
	out := make([]packetCandidate, 0)
	for _, symbol := range snapshot.Symbols {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, ok := candidateFromSymbol(symbol, contents, corpora)
		if !ok {
			continue
		}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].refs != out[j].refs {
			return out[i].refs < out[j].refs
		}
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].name < out[j].name
	})
	return out, nil
}

func candidateFromSymbol(symbol analyze.Symbol, contents map[string]string, corpora candidateCorpus) (packetCandidate, bool) {
	if (symbol.Kind != candidateFunction && symbol.Kind != candidateMethod) || strings.HasSuffix(symbol.Location.Path, "_test.go") {
		return packetCandidate{}, false
	}
	content, ok := contents[symbol.Location.Path]
	if !ok {
		return packetCandidate{}, false
	}
	refs := lexicalReferences(corpora.source, symbol.Name) - 1
	if refs < 0 {
		refs = 0
	}
	candidate := packetCandidate{
		path:      symbol.Location.Path,
		name:      symbol.Name,
		line:      symbol.Location.Line,
		refs:      refs,
		testRefs:  lexicalReferences(corpora.tests, symbol.Name),
		bodyLines: symbol.EndLine - symbol.Location.Line + 1,
		snippet:   candidateSnippet(content, symbol.Location.Line),
		exported:  symbol.Exported,
		method:    symbol.Kind == candidateMethod,
		kind:      candidateKind(symbol.Kind),
	}
	candidate.score = candidateScore(candidate.exported, candidate.method, candidate.refs, candidate.bodyLines)
	return candidate, true
}

func candidatePacket(candidates []packetCandidate) CandidatePacket {
	packet := CandidatePacket{SkippedSignals: []string{"semantic_references_not_checked", "git_history_not_checked"}}
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate.score <= 0 || seen[candidate.path] {
			continue
		}
		seen[candidate.path] = true
		packet.Files = append(packet.Files, CandidateFile{
			Path:           candidate.path,
			Lane:           "refactor-surface",
			Confidence:     candidateConfidence(candidate),
			Actionability:  candidateActionability(candidate),
			EvidenceLayers: []string{"ast_symbol_scan", "lexical_reference_count", "package_local_surface"},
			Reasons: []string{
				"candidate " + candidate.kind + " " + candidate.name + " at " + candidate.path + ":" + strconv.Itoa(candidate.line) + ":1",
				"lexical references outside declaration: " + strconv.Itoa(candidate.refs),
				"lexical references in tests: " + strconv.Itoa(candidate.testRefs),
				"body lines: " + strconv.Itoa(candidate.bodyLines),
				"declaration: " + candidate.snippet,
				"score: " + strconv.Itoa(candidate.score),
			},
			Verify: []string{"go test ./" + filepath.ToSlash(filepath.Dir(candidate.path))},
		})
		if len(packet.Files) == maxCandidateFiles {
			break
		}
	}
	if len(packet.Files) == 0 {
		packet.SkippedSignals = append(packet.SkippedSignals, "no_low_risk_refactor_candidates")
	}
	return packet
}

func lexicalReferences(corpus, name string) int {
	count := 0
	for _, word := range strings.FieldsFunc(corpus, func(r rune) bool {
		return !candidateWordRune(r)
	}) {
		if word == name {
			count++
		}
	}
	return count
}

func candidateWordRune(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func candidateSnippet(contents string, line int) string {
	lines := strings.Split(contents, "\n")
	if line <= 0 || line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

func candidateKind(kind string) string {
	if kind == candidateMethod {
		return candidateMethod
	}
	return "func"
}

func candidateScore(exported, method bool, refs, bodyLines int) int {
	score := 20
	if !exported {
		score += 15
	}
	if !method {
		score += 5
	}
	switch {
	case refs == 0:
		score += 40
	case refs == 1:
		score += 20
	case refs <= 3:
		score += 5
	default:
		score -= refs * 4
	}
	switch {
	case bodyLines <= 4:
		score += 4
	case bodyLines <= 20:
		score += 10
	case bodyLines <= 60:
		score += 4
	default:
		score -= 10
	}
	if score < 0 {
		return 0
	}
	return score
}

func candidateConfidence(candidate packetCandidate) string {
	if candidate.refs == 0 && !candidate.exported {
		return "high"
	}
	if candidate.refs <= 2 {
		return "medium"
	}
	return "low"
}

func candidateActionability(candidate packetCandidate) string {
	if candidate.refs == 0 && !candidate.exported {
		return "likely_defect"
	}
	if candidate.refs <= 2 {
		return "verify_first"
	}
	return candidateActionInspect
}
