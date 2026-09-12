package scan

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// maxOrphansPerBucket bounds each orphan list; totals stay observable
// through Truncation records, mirroring the other bounded packets.
const maxOrphansPerBucket = 100

// orphanCaveat is stamped on every orphan report: lexical counting in one
// repository can never see external or dispatch-table consumers.
const orphanCaveat = "Candidates only — pan counts lexical references in one repository. Verify external, reflect, and framework-dispatch consumers before deleting."

// OrphanCandidate is one exported symbol whose inbound references, counted
// lexically across every scanned Go file, are zero or test-only.
type OrphanCandidate struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Receiver   string `json:"receiver,omitempty"`
	Package    string `json:"package,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Confidence string `json:"confidence"`
}

// OrphanReport buckets exported symbols by inbound-reference status.
type OrphanReport struct {
	SchemaVersion string            `json:"schema_version"`
	Caveat        string            `json:"caveat"`
	Exported      int               `json:"exported_symbols"`
	ZeroRefs      []OrphanCandidate `json:"zero_refs"`
	TestOnlyRefs  []OrphanCandidate `json:"test_only_refs"`
	Truncations   []Truncation      `json:"truncations,omitempty"`
}

// ReferenceResult is one semantic reference lookup for an exported symbol.
// Available is false when the configured language server cannot answer; the
// caller then retains the conservative lexical classification.
type ReferenceResult struct {
	Available bool
	NonTest   int
	Test      int
}

// ReferenceLookup resolves references for a declaration candidate.
type ReferenceLookup func(context.Context, OrphanCandidate) (ReferenceResult, error)

// orphanCounts aggregates per-name identifier occurrences and top-level
// declaration counts, split between non-test and test files so reference
// classification can distinguish production use from test-only use.
type orphanCounts struct {
	occurrences [2]int // [non-test, test]
	decls       [2]int // [non-test, test]
}

type orphanClass uint8

const (
	orphanUsed orphanClass = iota
	orphanZero
	orphanTestOnly
)

type semanticClassification struct {
	available bool
	zero      bool
	testOnly  bool
}

const (
	idxNonTest = 0
	idxTest    = 1
)

// Orphans buckets exported symbols from non-test, non-generated files by
// inbound lexical reference status. Counting is deliberately conservative:
// every identifier occurrence that is not a top-level declaration counts as
// a reference, including interface method names, field names, and selector
// suffixes, so only names the repository never mentions surface as
// candidates. Entry points (main) are excluded. top > 0 caps the zero-ref
// list; test-only refs are always capped at maxOrphansPerBucket.
func Orphans(ctx context.Context, snap analyze.Snapshot, top int) (OrphanReport, error) {
	return OrphansWithReferences(ctx, snap, top, nil)
}

// OrphansWithReferences prefers semantic language-server references when
// available and falls back to the conservative lexical evidence otherwise.
func OrphansWithReferences(ctx context.Context, snap analyze.Snapshot, top int, lookup ReferenceLookup) (OrphanReport, error) {
	generated := generatedPaths(snap.Files)
	counts := map[string]*orphanCounts{}
	get := func(name string) *orphanCounts {
		entry, ok := counts[name]
		if !ok {
			entry = &orphanCounts{}
			counts[name] = entry
		}
		return entry
	}

	// One parse per Go file in the snapshot (tests included: their
	// references classify as test-only).
	fset := token.NewFileSet()
	for _, file := range snap.Files {
		if file.Language != languageGo {
			continue
		}
		if err := ctx.Err(); err != nil {
			return OrphanReport{}, err
		}
		idx := idxTest
		if !isTestPath(file.Path) {
			idx = idxNonTest
		}
		parsed, err := parser.ParseFile(fset, path.Join(snap.Root, filepathFromSlash(file.Path)), nil, 0)
		if err != nil {
			return OrphanReport{}, fmt.Errorf("scan orphans parse %s: %w", file.Path, err)
		}
		countReferences(parsed, get, idx)
	}

	candidates := orphanCandidates(snap, generated)
	var zero, testOnly []OrphanCandidate
	for _, candidate := range candidates {
		entry := counts[candidate.Name]
		if entry == nil {
			entry = &orphanCounts{}
		}
		switch classifyOrphan(ctx, lookup, candidate, entry) {
		case orphanZero:
			zero = append(zero, candidate)
		case orphanTestOnly:
			testOnly = append(testOnly, candidate)
		case orphanUsed:
		}
	}

	report := OrphanReport{
		SchemaVersion: snap.SchemaVersion,
		Caveat:        orphanCaveat,
		Exported:      len(candidates),
		Truncations:   []Truncation{},
	}
	zero, report.Truncations = capOrphans(zero, "zero_refs", "orphan bucket cap", report.Truncations)
	if top > 0 && len(zero) > top {
		report.Truncations = append(report.Truncations, Truncation{
			Field: "zero_refs", Shown: top, Total: len(zero), Reason: "truncated by --top",
		})
		zero = zero[:top]
	}
	testOnly, report.Truncations = capOrphans(testOnly, "test_only_refs", "orphan bucket cap", report.Truncations)
	report.ZeroRefs = zero
	report.TestOnlyRefs = testOnly
	return report, nil
}

func classifyOrphan(ctx context.Context, lookup ReferenceLookup, candidate OrphanCandidate, counts *orphanCounts) orphanClass {
	semantic := semanticOrphan(ctx, lookup, candidate)
	if semantic.available {
		if semantic.zero {
			return orphanZero
		}
		if semantic.testOnly {
			return orphanTestOnly
		}
		return orphanUsed
	}
	nonTestRefs := counts.occurrences[idxNonTest] - counts.decls[idxNonTest]
	testRefs := counts.occurrences[idxTest] - counts.decls[idxTest]
	if nonTestRefs == 0 && testRefs == 0 {
		return orphanZero
	}
	if nonTestRefs == 0 && testRefs > 0 {
		return orphanTestOnly
	}
	return orphanUsed
}

func semanticOrphan(ctx context.Context, lookup ReferenceLookup, candidate OrphanCandidate) semanticClassification {
	if lookup == nil || candidate.Package == "" {
		return semanticClassification{}
	}
	refs, err := lookup(ctx, candidate)
	if err != nil || !refs.Available {
		return semanticClassification{}
	}
	return semanticClassification{
		available: true,
		zero:      refs.NonTest == 0 && refs.Test == 0,
		testOnly:  refs.NonTest == 0 && refs.Test > 0,
	}
}

// capOrphans truncates one bucket to maxOrphansPerBucket and records the
// truncation so the total stays observable.
func capOrphans(candidates []OrphanCandidate, field, reason string, truncations []Truncation) ([]OrphanCandidate, []Truncation) {
	if len(candidates) <= maxOrphansPerBucket {
		return candidates, truncations
	}
	truncations = append(truncations, Truncation{
		Field: field, Shown: maxOrphansPerBucket, Total: len(candidates), Reason: reason,
	})
	return candidates[:maxOrphansPerBucket:maxOrphansPerBucket], truncations
}

// orphanCandidates selects the exported symbols eligible for orphan
// classification: non-test, non-generated declaration sites with a known
// line, excluding the main entry point. Results keep the snapshot's sorted
// symbol order (file, then line), which is the deterministic report order.
func orphanCandidates(snap analyze.Snapshot, generated map[string]bool) []OrphanCandidate {
	var candidates []OrphanCandidate
	for _, symbol := range snap.Symbols {
		if !symbol.Exported || symbol.Name == "main" || symbol.Location.Line <= 0 {
			continue
		}
		if isTestPath(symbol.Location.Path) || generated[symbol.Location.Path] {
			continue
		}
		candidates = append(candidates, OrphanCandidate{
			Name:       symbol.Name,
			Kind:       symbol.Kind,
			Receiver:   symbol.Receiver,
			Package:    symbol.Package,
			File:       symbol.Location.Path,
			Line:       symbol.Location.Line,
			Confidence: analyze.ConfidenceLexical,
		})
	}
	return candidates
}

// countReferences adds every identifier occurrence in file to the counter
// for name at idx, then adds one declaration count per top-level
// declaration of that name. Occurrences minus declarations therefore count
// references, with declaration sites in foreign packages (same-named
// declarations) conservatively absorbed rather than misattributed.
func countReferences(file *ast.File, get func(string) *orphanCounts, idx int) {
	ast.Inspect(file, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name != "_" && ident.Name != "." {
			get(ident.Name).occurrences[idx]++
		}
		return true
	})
	for _, decl := range file.Decls {
		for _, name := range declaredNames(decl) {
			get(name).decls[idx]++
		}
	}
}

// declaredNames lists the top-level names declared by one declaration.
// Method names are included; interface method and struct field names are
// not declarations here, so they count as references.
func declaredNames(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []string{d.Name.Name}
	case *ast.GenDecl:
		if d.Tok.String() == "import" {
			return nil
		}
		var names []string
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, ident := range s.Names {
					names = append(names, ident.Name)
				}
			}
		}
		return names
	}
	return nil
}

// generatedPaths indexes the snapshot's generated-file flags by path.
func generatedPaths(files []analyze.File) map[string]bool {
	generated := make(map[string]bool, len(files))
	for _, file := range files {
		if file.Generated {
			generated[file.Path] = true
		}
	}
	return generated
}

// EffectKinds returns the deterministic set of side-effect kind names the
// effects packet can report, sorted. Callers use it to validate kind
// filters without duplicating the pattern table.
func EffectKinds() []string {
	patterns := effectTable()
	kinds := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		kinds = append(kinds, pattern.Kind)
	}
	slices.Sort(kinds)
	return kinds
}

// FilterKind restricts an effects report to one effect kind. Files without
// a matching effect drop out, lanes recompute for the kept kind, and the
// kinds summary keeps only the requested kind so counts stay consistent
// with the visible files.
func (r EffectsReport) FilterKind(kind string) EffectsReport {
	patterns := effectTable()
	out := EffectsReport{}
	for _, file := range r.Files {
		var kept []Effect
		for _, effect := range file.Effects {
			if effect.Kind == kind {
				kept = append(kept, effect)
			}
		}
		if len(kept) == 0 {
			continue
		}
		out.Files = append(out.Files, EffectFile{
			Path:    file.Path,
			Lanes:   []string{effectKindLane(kind, patterns)},
			Effects: kept,
		})
	}
	for _, summary := range r.Kinds {
		if summary.Name == kind {
			out.Kinds = append(out.Kinds, summary)
			break
		}
	}
	if len(out.Files) == 0 {
		out.FilesOmittedReason = "no files carry the requested effect kind"
	}
	slices.SortFunc(out.Files, func(a, b EffectFile) int {
		if len(a.Effects) != len(b.Effects) {
			return len(b.Effects) - len(a.Effects)
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}
