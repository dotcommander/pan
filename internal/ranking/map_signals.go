package ranking

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/dotcommander/pan/internal/analyze"
)

const (
	symbolRefsBonusPerRef = 2
	symbolRefsMaxBonus    = 24
)

// ApplySymbolReferenceBonus adds bounded lexical cross-file references for
// exported non-Go names. The contribution remains separately explainable.
func ApplySymbolReferenceBonus(root string, ranked []RankedFile) {
	byName := referenceTargets(ranked)
	if len(byName) == 0 {
		return
	}
	removeCommonTargets(byName, len(ranked))
	applyReferenceBonuses(ranked, findReferences(root, ranked, byName))
	sortRanked(ranked)
}

type referenceTarget struct{ path string }

func referenceTargets(ranked []RankedFile) map[string][]referenceTarget {
	byName := make(map[string][]referenceTarget)
	for i := range ranked {
		if ranked[i].Language == languageGo {
			continue
		}
		for _, symbol := range ranked[i].Symbols {
			if symbol.Exported && referenceNameOK(symbol.Name) {
				byName[symbol.Name] = append(byName[symbol.Name], referenceTarget{path: ranked[i].Path})
			}
		}
	}
	return byName
}

func removeCommonTargets(byName map[string][]referenceTarget, files int) {
	limit := files * 40 / 100
	if limit < 2 {
		return
	}
	for name, targets := range byName {
		if len(targets) > limit {
			delete(byName, name)
		}
	}
}

func findReferences(root string, ranked []RankedFile, byName map[string][]referenceTarget) map[string]map[string]struct{} {
	refs := make(map[string]map[string]struct{})
	for _, file := range ranked {
		words, ok := identifierSet(filepath.Join(root, file.Path))
		if !ok {
			continue
		}
		for word := range words {
			for _, target := range byName[word] {
				if target.path == file.Path {
					continue
				}
				if refs[target.path] == nil {
					refs[target.path] = make(map[string]struct{})
				}
				refs[target.path][file.Path] = struct{}{}
			}
		}
	}
	return refs
}

func applyReferenceBonuses(ranked []RankedFile, refs map[string]map[string]struct{}) {
	for i := range ranked {
		bonus := len(refs[ranked[i].Path]) * symbolRefsBonusPerRef
		if bonus > symbolRefsMaxBonus {
			bonus = symbolRefsMaxBonus
		}
		if bonus > 0 {
			addComponent(&ranked[i], "symbol_refs", bonus)
		}
	}
}

// ApplyCallEdgeBonus promotes files with bounded call evidence.
func ApplyCallEdgeBonus(ranked []RankedFile, edges []analyze.Edge, threshold int, includeTests bool) {
	targets := edgeTargets(ranked, max(0, threshold))
	callers := edgeCallers(len(ranked), edges, targets, includeTests)
	applyCallerBonuses(ranked, callers)
	sortRanked(ranked)
}

func edgeTargets(ranked []RankedFile, threshold int) map[string][]int {
	targets := make(map[string][]int)
	for i := range ranked {
		if ranked[i].ImportedBy < threshold {
			continue
		}
		for _, symbol := range ranked[i].Symbols {
			if symbol.Exported {
				targets[symbol.Name] = append(targets[symbol.Name], i)
			}
		}
	}
	return targets
}

func edgeCallers(size int, edges []analyze.Edge, targets map[string][]int, includeTests bool) []map[string]struct{} {
	callers := make([]map[string]struct{}, size)
	for _, edge := range edges {
		if edge.Kind != edgeCalls || edge.Confidence != analyze.ConfidenceConfirmed || (!includeTests && strings.HasSuffix(edge.Location.Path, "_test.go")) {
			continue
		}
		for _, index := range targets[edge.To] {
			if callers[index] == nil {
				callers[index] = make(map[string]struct{})
			}
			callers[index][edge.Location.Path] = struct{}{}
		}
	}
	return callers
}

func applyCallerBonuses(ranked []RankedFile, callers []map[string]struct{}) {
	for i := range ranked {
		if count := len(callers[i]); count > 0 {
			addComponent(&ranked[i], ComponentCallers, min(20, count*2))
		}
	}
}

func sortRanked(ranked []RankedFile) {
	slices.SortStableFunc(ranked, func(a, b RankedFile) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Path, b.Path)
	})
}

func referenceNameOK(name string) bool {
	if len(name) < 4 {
		return false
	}
	for i, r := range name {
		if (i == 0 && r != '_' && !unicode.IsLetter(r)) || (i > 0 && r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func identifierSet(name string) (map[string]struct{}, bool) {
	data, err := os.ReadFile(name)
	if err != nil || len(data) > 1<<20 {
		return nil, false
	}
	words := make(map[string]struct{})
	var word strings.Builder
	flush := func() {
		if word.Len() != 0 {
			words[word.String()] = struct{}{}
			word.Reset()
		}
	}
	for _, r := range string(data) {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words, true
}
