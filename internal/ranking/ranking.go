// Package ranking turns an analyze.Snapshot into a deterministic, importance
// ranked file list. Scores are decomposed into named components so `context
// explain` can report exactly why a file ranked the way it did; every
// component carries a confidence tier (confirmed, lexical, or contextual)
// describing the kind of evidence behind it.
package ranking

import (
	"math"
	"path"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/fileclass"
)

// Confidence tiers for score components, in canonical display order.
const (
	TierConfirmed  = "confirmed"  // structural facts from AST or file identity
	TierLexical    = "lexical"    // by-name matching that may be coincidental
	TierContextual = "contextual" // query-dependent evidence
)

// TierOf reports the confidence tier of a score component key.
func TierOf(component string) string {
	switch component {
	case ComponentCallers:
		return TierLexical
	case ComponentIntent, ComponentConsumed:
		return TierContextual
	default:
		return TierConfirmed
	}
}

const (
	edgeCalls = "calls"
)

// Score component keys. These are a stability surface: `context explain`
// output and cache-busting documentation name them directly.
const (
	ComponentEntry          = "entry"
	ComponentOrientation    = "orientation"
	ComponentSymbols        = "symbols"
	ComponentDepth          = "depth"
	ComponentImports        = "imports"
	ComponentCallers        = "callers"
	ComponentTests          = "tests"
	ComponentTestDemote     = "test_demote"
	ComponentIntent         = "intent"
	ComponentConsumed       = "consumed"
	ComponentReferenceGraph = "reference_graph"
	// ComponentClassDemote records bounded demotions for repository file roles
	// that rarely own a code task: examples, vendor, generated, docs, compat
	// and legacy shims, and test-classed files the Go suffix check cannot see.
	ComponentClassDemote = "class_demote"
)

// RankedFile is one analyzed file with its importance score decomposition.
type RankedFile struct {
	Path     string
	Language string
	Package  string

	Symbols []analyze.Symbol
	// Imports holds raw import paths observed for this file (Go only).
	Imports []string
	// InternalImports resolves those imports to repo-relative directories;
	// entries only appear when the import target exists in the snapshot.
	InternalImports []string

	Score      int
	Components map[string]int

	Tag         string // "entry" for detected entry points
	ImportedBy  int    // files importing this file's package
	DependsOn   int    // internal packages this file's package imports
	DetailLevel int    // assigned by AssignBudget
	TestFile    bool
	Tested      bool            // package directory has at least one test file
	IntentHits  int             // intent terms matched in path or symbol names
	Consumed    bool            // listed in Options.Consumed
	Class       fileclass.Class `json:"class"`
	CallerCount int             `json:"caller_count,omitempty"`
	Confidence  string          `json:"confidence,omitempty"`
}

// Options shapes one ranking pass.
type Options struct {
	// Intent orients the ranking toward a task; empty disables the contextual
	// intent component entirely.
	Intent string
	// Consumed are repo-relative paths already in the agent's context. They
	// rank up but task packets omit their source.
	Consumed []string
	// IncludeTests retains test files at full weight instead of the default demotion.
	IncludeTests bool
	// ReferenceGraph enables the optional structural-reference-graph/v1
	// component. It is intentionally off for the default policy.
	ReferenceGraph bool
}

// GraphStats describes one deterministic reference-graph scoring pass.
type GraphStats struct {
	Nodes      int `json:"nodes"`
	Edges      int `json:"edges"`
	Iterations int `json:"iterations"`
}

// Rank scores and sorts every file in snap. Files sort by score descending,
// then path ascending, so equal scores keep a stable order. modulePath is the
// target's Go module path ("" when absent); it resolves import edges to
// repository directories, falling back to suffix matching without it.
func Rank(snap analyze.Snapshot, modulePath string, opts Options) []RankedFile {
	ranked, _ := RankWithStats(snap, modulePath, opts)
	return ranked
}

// RankWithStats scores files and returns optional graph-policy telemetry.
func RankWithStats(snap analyze.Snapshot, modulePath string, opts Options) ([]RankedFile, GraphStats) {
	grouped := groupSnapshot(snap)
	ranked := make([]RankedFile, 0, len(grouped))
	for _, f := range snap.Files {
		g := grouped[f.Path]
		if g == nil {
			g = &fileGroup{}
		}
		rf := RankedFile{
			Path:       f.Path,
			Language:   f.Language,
			Symbols:    g.symbols,
			Imports:    g.imports,
			TestFile:   strings.HasSuffix(f.Path, "_test.go"),
			Components: make(map[string]int),
			Class:      fileclass.Classify(f.Path, f.Language, f.Generated),
			Confidence: analyze.ConfidenceConfirmed,
		}
		if len(g.symbols) > 0 {
			rf.Package = g.symbols[0].Package
		}
		ranked = append(ranked, rf)
	}

	testDirs := make(map[string]bool)
	for _, rf := range ranked {
		if rf.TestFile {
			testDirs[path.Dir(rf.Path)] = true
		}
	}
	for i := range ranked {
		ranked[i].Tested = testDirs[path.Dir(ranked[i].Path)]
	}

	applyEntryBoosts(ranked)
	applyOrientationBoosts(ranked)
	applySymbolBonus(ranked)
	applyDepthPenalty(ranked)
	applyImportScores(ranked, modulePath)
	applyCallerScores(ranked, snap)
	applyTestSignals(ranked, opts.IncludeTests)
	applyClassSignals(ranked, opts.IncludeTests)
	applyIntentScores(ranked, opts.Intent)
	applyConsumedBoosts(ranked, opts.Consumed)
	stats := GraphStats{}
	if opts.ReferenceGraph {
		stats = applyReferenceGraphScores(ranked, snap)
	}

	slices.SortFunc(ranked, func(a, b RankedFile) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Path, b.Path)
	})
	return ranked, stats
}

const (
	referenceGraphDamping       = 0.85
	referenceGraphTolerance     = 1e-9
	referenceGraphMaxIterations = 40
	referenceGraphMaxBonus      = 30
	edgeKindReferences          = "references"
)

// referenceGraphPair identifies one directed ranked-file pair.
type referenceGraphPair struct{ from, to int }

func applyReferenceGraphScores(ranked []RankedFile, snap analyze.Snapshot) GraphStats {
	index := make(map[string]int, len(ranked))
	for i := range ranked {
		index[ranked[i].Path] = i
	}
	counts, ambiguity := referenceGraphCounts(index, snap)
	if len(counts) == 0 || len(ranked) == 0 {
		return GraphStats{}
	}
	weights, pairs, outgoing := referenceGraphWeights(counts, ambiguity, len(ranked))
	scores, iterations := iterateReferenceGraph(pairs, weights, outgoing, len(ranked))
	maxScore := 0.0
	for _, score := range scores {
		maxScore = max(maxScore, score)
	}
	for i, score := range scores {
		bonus := int(math.Round(referenceGraphMaxBonus * score / maxScore))
		if bonus > 0 {
			addComponent(&ranked[i], ComponentReferenceGraph, bonus)
		}
	}
	return GraphStats{Nodes: len(ranked), Edges: len(counts), Iterations: iterations}
}

// referenceGraphCounts tallies reference edges between ranked files and
// tracks each edge's symbol-definition ambiguity.
func referenceGraphCounts(index map[string]int, snap analyze.Snapshot) (counts map[referenceGraphPair]int, ambiguity map[referenceGraphPair]int) {
	definitions := make(map[string]int)
	for _, symbol := range snap.Symbols {
		definitions[symbol.Name]++
	}
	counts = make(map[referenceGraphPair]int)
	ambiguity = make(map[referenceGraphPair]int)
	for _, edge := range snap.Edges {
		if edge.Kind != edgeKindReferences {
			continue
		}
		from, fromOK := index[edge.From]
		to, toOK := index[edge.To]
		if fromOK && toOK && from != to {
			p := referenceGraphPair{from, to}
			counts[p]++
			ambiguity[p] = max(ambiguity[p], definitions[edge.Symbol])
		}
	}
	return counts, ambiguity
}

// referenceGraphWeights derives edge weights from counts and ambiguity,
// orders the pair list, and accumulates each file's outgoing weight.
func referenceGraphWeights(counts, ambiguity map[referenceGraphPair]int, n int) (map[referenceGraphPair]float64, []referenceGraphPair, []float64) {
	weights := make(map[referenceGraphPair]float64, len(counts))
	pairs := make([]referenceGraphPair, 0, len(counts))
	outgoing := make([]float64, n)
	for p, count := range counts {
		pairs = append(pairs, p)
		definitionCount := ambiguity[p]
		if definitionCount < 1 {
			definitionCount = 1
		}
		weight := math.Sqrt(float64(count)) / math.Sqrt(float64(definitionCount))
		weights[p] = weight
	}
	slices.SortFunc(pairs, func(a, b referenceGraphPair) int {
		if a.from != b.from {
			return a.from - b.from
		}
		return a.to - b.to
	})
	for _, p := range pairs {
		outgoing[p.from] += weights[p]
	}
	return weights, pairs, outgoing
}

// iterateReferenceGraph runs the damped reference-graph score propagation
// until the tolerance or iteration bound is reached.
func iterateReferenceGraph(pairs []referenceGraphPair, weights map[referenceGraphPair]float64, outgoing []float64, n int) ([]float64, int) {
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = 1 / float64(n)
	}
	iterations := 0
	for ; iterations < referenceGraphMaxIterations; iterations++ {
		next := make([]float64, n)
		dangling := 0.0
		for i, score := range scores {
			if outgoing[i] == 0 {
				dangling += score
			}
		}
		base := (1-referenceGraphDamping)/float64(n) + referenceGraphDamping*dangling/float64(n)
		for i := range next {
			next[i] = base
		}
		for _, p := range pairs {
			weight := weights[p]
			next[p.to] += referenceGraphDamping * scores[p.from] * weight / outgoing[p.from]
		}
		delta := 0.0
		for i := range next {
			delta += math.Abs(next[i] - scores[i])
		}
		scores = next
		if delta < referenceGraphTolerance {
			iterations++
			break
		}
	}
	return scores, iterations
}

// fileGroup collects snapshot evidence for one file path.
type fileGroup struct {
	symbols []analyze.Symbol
	imports []string
}

func groupSnapshot(snap analyze.Snapshot) map[string]*fileGroup {
	grouped := make(map[string]*fileGroup, len(snap.Files))
	ensure := func(p string) *fileGroup {
		g, ok := grouped[p]
		if !ok {
			g = &fileGroup{}
			grouped[p] = g
		}
		return g
	}
	for _, symbol := range snap.Symbols {
		ensure(symbol.Location.Path).symbols = append(ensure(symbol.Location.Path).symbols, symbol)
	}
	for _, edge := range snap.Edges {
		if edge.Kind == "imports" {
			ensure(edge.From).imports = append(ensure(edge.From).imports, edge.To)
		}
	}
	return grouped
}

func addComponent(rf *RankedFile, key string, delta int) {
	if delta == 0 {
		return
	}
	rf.Score += delta
	rf.Components[key] += delta
}

// entryNameBoost returns the orientation boost for a language-conventional
// entry file name. ok is false when base is not an entry file name.
func entryNameBoost(base string) (int, bool) {
	switch base {
	case "main.go":
		return 40, true
	case "main.ts", "index.ts", "index.js":
		return 30, true
	case "app.py", "main.py", "main.rs":
		return 30, true
	default:
		return 0, false
	}
}

func applyEntryBoosts(ranked []RankedFile) {
	for i := range ranked {
		boost, ok := entryNameBoost(path.Base(ranked[i].Path))
		if !ok {
			continue
		}
		addComponent(&ranked[i], ComponentEntry, boost)
		ranked[i].Tag = "entry"
	}
}

func applyOrientationBoosts(ranked []RankedFile) {
	for i := range ranked {
		rf := &ranked[i]
		base := path.Base(rf.Path)
		boost := 0
		if rf.Package != "" && rf.Language == analyze.LanguageGo && strings.TrimSuffix(base, path.Ext(base)) == rf.Package {
			boost += 25
		}
		if base == "types.go" {
			boost += 20
		}
		if !strings.Contains(rf.Path, "/") && path.Ext(rf.Path) == ".go" && !rf.TestFile {
			boost += 8
		}
		if boost > 35 {
			boost = 35
		}
		addComponent(rf, ComponentOrientation, boost)
	}
}

// symbolKindWeight values contract-defining symbol kinds above behavior
// and data; unknown kinds weigh as behavior.
func symbolKindWeight(kind string) int {
	switch kind {
	case "interface", "type":
		return 3
	case "struct":
		return 2
	case "constant", "variable":
		return 0
	default:
		return 1
	}
}

const maxSymbolBonus = 20

func applySymbolBonus(ranked []RankedFile) {
	for i := range ranked {
		rf := &ranked[i]
		bonus := 0
		for _, symbol := range rf.Symbols {
			if !symbol.Exported {
				continue
			}
			bonus += symbolKindWeight(symbol.Kind)
		}
		if bonus > maxSymbolBonus {
			bonus = maxSymbolBonus
		}
		addComponent(rf, ComponentSymbols, bonus)
	}
}

func applyDepthPenalty(ranked []RankedFile) {
	for i := range ranked {
		depth := strings.Count(ranked[i].Path, "/")
		if depth > 2 {
			addComponent(&ranked[i], ComponentDepth, -(depth - 2))
		}
	}
}

// importScoreKnee is the importer count past which each additional importer
// adds only +1 instead of +10, so one hub cannot monopolize the ranking.
const importScoreKnee = 8

func importScore(count int) int {
	if count <= importScoreKnee {
		return count * 10
	}
	return importScoreKnee*10 + (count - importScoreKnee)
}

// applyImportScores resolves each Go file's package directory to a module
// import key, counts unique importers per key, and distributes the import
// component. Directories are also wired into InternalImports/DependsOn for
// map and task rendering.
func applyImportScores(ranked []RankedFile, modulePath string) {
	keys := importKeys(ranked, modulePath)
	internalDirs := internalDirSet(keys)
	importers := countImporters(ranked, modulePath, internalDirs)
	distributeImportScores(ranked, keys, importers)
}

// dirImportKey maps a repo-relative file path to its package directory,
// with "" for repository-root files.
func dirImportKey(p string) string {
	if dir := path.Dir(p); dir != "." {
		return dir
	}
	return ""
}

// fullImportKey maps a package directory to its module-qualified import key.
func fullImportKey(dir, modulePath string) string {
	if dir == "" {
		return modulePath
	}
	return modulePath + "/" + dir
}

// importKeys maps each module-qualified package import key to the ranked
// indices of the files that declare it. Without a module path there are no
// resolvable keys.
func importKeys(ranked []RankedFile, modulePath string) map[string][]int {
	keys := make(map[string][]int, len(ranked))
	if modulePath == "" {
		return keys
	}
	for i := range ranked {
		if ranked[i].Language != analyze.LanguageGo {
			continue
		}
		key := fullImportKey(dirImportKey(ranked[i].Path), modulePath)
		keys[key] = append(keys[key], i)
	}
	return keys
}

// internalDirSet collects the import keys as the set of internal directories.
func internalDirSet(keys map[string][]int) map[string]struct{} {
	dirs := make(map[string]struct{}, len(keys))
	for key := range keys {
		dirs[key] = struct{}{}
	}
	return dirs
}

// countImporters wires each Go file's internal imports into InternalImports
// and DependsOn, and returns the unique importer file paths per import key.
func countImporters(ranked []RankedFile, modulePath string, internalDirs map[string]struct{}) map[string]map[string]struct{} {
	importers := make(map[string]map[string]struct{}) // key -> importing file paths
	for i := range ranked {
		rf := &ranked[i]
		if rf.Language != analyze.LanguageGo {
			continue
		}
		seen := make(map[string]struct{})
		for _, imp := range rf.Imports {
			dir, ok := resolveImportDir(imp, modulePath, internalDirs)
			if !ok {
				continue
			}
			if _, dup := seen[dir]; dup {
				continue
			}
			seen[dir] = struct{}{}
			rf.InternalImports = append(rf.InternalImports, dir)
			key := dir
			if modulePath != "" {
				key = fullImportKey(dir, modulePath)
			}
			if importers[key] == nil {
				importers[key] = make(map[string]struct{})
			}
			importers[key][rf.Path] = struct{}{}
		}
		slices.Sort(rf.InternalImports)
		rf.DependsOn = len(rf.InternalImports)
	}
	return importers
}

// resolveImportDir maps one import path to the internal directory it
// references: a module-prefixed import resolves by trimming the prefix,
// otherwise an exact suffix match against known internal directories is
// accepted. ok is false when the import is not internal to the module.
func resolveImportDir(imp, modulePath string, internalDirs map[string]struct{}) (string, bool) {
	var dir string
	switch {
	case modulePath != "" && strings.HasPrefix(imp, modulePath+"/"):
		dir = strings.TrimPrefix(imp, modulePath+"/")
	case modulePath != "" && imp == modulePath:
		dir = "."
	default:
		// No module prefix: accept an exact suffix match against known
		// internal directories (basename-style resolution).
		if !resolveSuffix(imp, internalDirs, &dir) {
			return "", false
		}
	}
	if _, known := internalDirs[dir]; !known {
		return "", false
	}
	if dir == "" {
		dir = "."
	}
	return dir, true
}

// distributeImportScores assigns the import component and ImportedBy count
// to every file declaring an imported key.
func distributeImportScores(ranked []RankedFile, keys map[string][]int, importers map[string]map[string]struct{}) {
	for key, sources := range importers {
		count := len(sources)
		for _, i := range keys[key] {
			addComponent(&ranked[i], ComponentImports, importScore(count))
			ranked[i].ImportedBy = count
		}
	}
}

// resolveSuffix finds the internal directory whose path is the suffix of imp.
func resolveSuffix(imp string, dirs map[string]struct{}, out *string) bool {
	best := ""
	for dir := range dirs {
		if dir != "." && (imp == dir || strings.HasSuffix(imp, "/"+dir)) &&
			(len(dir) > len(best) || (len(dir) == len(best) && dir < best)) {
			best = dir
		}
	}
	if best == "" {
		return false
	}
	*out = best
	return true
}

func applyTestSignals(ranked []RankedFile, includeTests bool) {
	for i := range ranked {
		if ranked[i].TestFile && !includeTests {
			addComponent(&ranked[i], ComponentTestDemote, -15)
		} else if ranked[i].Tested {
			addComponent(&ranked[i], ComponentTests, 4)
		}
	}
}

// classDemotion values mirror demotion practice for retrieval: tests,
// compat shims, and example code are strong noise; generated and vendor
// files are stronger noise; docs are mild noise for code-oriented ranking.
const (
	classDemoteTest     = -10
	classDemoteCompat   = -12
	classDemoteExample  = -12
	classDemoteVendor   = -20
	classDemoteGenerate = -20
	classDemoteDocs     = -6
)

// compatSegments are path segments that mark compatibility or legacy shims.
var compatSegments = map[string]struct{}{"compat": {}, "_compat": {}, "legacy": {}}

// hasCompatSegment reports whether any slash-delimited path segment names a
// compatibility or legacy directory.
func hasCompatSegment(p string) bool {
	for _, segment := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if _, ok := compatSegments[segment]; ok {
			return true
		}
	}
	return false
}

// applyClassSignals demotes non-production file roles after the structural
// components. includeTests restores full weight for test-classed files.
func applyClassSignals(ranked []RankedFile, includeTests bool) {
	for i := range ranked {
		rf := &ranked[i]
		demote := 0
		switch rf.Class {
		case fileclass.Example:
			demote += classDemoteExample
		case fileclass.Vendor:
			demote += classDemoteVendor
		case fileclass.Generated:
			demote += classDemoteGenerate
		case fileclass.Docs:
			demote += classDemoteDocs
		case fileclass.Test:
			if !includeTests && !rf.TestFile {
				demote += classDemoteTest
			}
		case fileclass.Fixture, fileclass.Data, fileclass.Production, fileclass.Unknown:
			// no class-based demotion for these roles
		}
		if hasCompatSegment(rf.Path) {
			demote += classDemoteCompat
		}
		if demote != 0 {
			addComponent(rf, ComponentClassDemote, demote)
		}
	}
}

// Intent match weights: exact part matches score full weight, prefix-overlap
// matches score half weight.
const (
	intentPathExact   = 4
	intentPathPrefix  = 2
	intentSymbolExact = 2
	intentSymbolHalf  = 1
)

func applyIntentScores(ranked []RankedFile, intent string) {
	terms := intentTerms(intent)
	if len(terms) == 0 {
		return
	}
	for i := range ranked {
		rf := &ranked[i]
		pathParts := PathTerms(rf.Path)
		symbolParts := make([][]string, len(rf.Symbols))
		for j, symbol := range rf.Symbols {
			symbolParts[j] = SplitIdentifier(symbol.Name)
		}
		boost, hitsPath, hitsSymbol := intentFileBoost(pathParts, symbolParts, terms)
		if boost > 0 {
			addComponent(rf, ComponentIntent, boost)
			rf.IntentHits = hitsPath + hitsSymbol
			rf.Confidence = analyze.ConfidenceHeuristic
		}
	}
}

// intentFileBoost scores one file's paths and symbols against the intent
// terms: exact part matches at full weight, prefix-overlap matches at half.
func intentFileBoost(pathParts []string, symbolParts [][]string, terms []string) (boost, hitsPath, hitsSymbol int) {
	for _, term := range terms {
		if matched, exact := MatchPart(pathParts, term); matched {
			if exact {
				boost += intentPathExact
			} else {
				boost += intentPathPrefix
			}
			hitsPath++
		}
		for j := range symbolParts {
			if matched, exact := MatchPart(symbolParts[j], term); matched {
				if exact {
					boost += intentSymbolExact
				} else {
					boost += intentSymbolHalf
				}
				hitsSymbol++
				break
			}
		}
	}
	if boost > 30 {
		boost = 30
	}
	return boost, hitsPath, hitsSymbol
}

func applyConsumedBoosts(ranked []RankedFile, consumed []string) {
	if len(consumed) == 0 {
		return
	}
	set := make(map[string]struct{}, len(consumed))
	for _, p := range consumed {
		set[p] = struct{}{}
	}
	for i := range ranked {
		if _, ok := set[ranked[i].Path]; ok {
			addComponent(&ranked[i], ComponentConsumed, 15)
			ranked[i].Consumed = true
		}
	}
}

// headerCostOverhead estimates the rendered bytes of one file header line:
// path plus annotation overhead and a newline.
const headerCostOverhead = 30

// headerBudgetShare is the fraction of the byte budget reserved for file
// headers before any symbol detail is spent; files whose headers cannot fit
// within it are omitted entirely and reported as truncations.
const headerBudgetShare = 70

// AssignBudget assigns every ranked file a rendering DetailLevel under a
// token budget (estimated as 4 bytes per token), using a breadth-first
// strategy: every file is first made visible (level 0 header or level 1
// summary) before any budget is spent on depth (level 2 full symbols).
// cost estimates the rendered byte cost of one file's full symbol block; the
// caller supplies the estimator that matches its renderer so budgeting and
// rendering cannot drift. maxTokens <= 0 assigns level 2 to every file
// (unlimited mode). ranked is mutated in place and also returned.
//
// Detail levels: -1 omitted, 0 header only, 1 counts summary, 2 full symbols.
func AssignBudget(ranked []RankedFile, maxTokens int, cost func([]analyze.Symbol) int) []RankedFile {
	if len(ranked) == 0 {
		return ranked
	}
	if maxTokens <= 0 {
		for i := range ranked {
			ranked[i].DetailLevel = 2
		}
		return ranked
	}
	budgetBytes := maxTokens * 4

	// Header pass: bound output under tiny budgets. Files whose header cannot
	// fit within the reserved share are the only ones omitted up front.
	headerCap := budgetBytes * headerBudgetShare / 100
	headerCost := 0
	cutoff := len(ranked)
	for i, f := range ranked {
		lineCost := len(f.Path) + headerCostOverhead
		if headerCost+lineCost > headerCap {
			cutoff = i
			break
		}
		headerCost += lineCost
	}
	for i := cutoff; i < len(ranked); i++ {
		ranked[i].DetailLevel = -1
	}

	// Breadth pass: reserve a one-line summary per file where it fits.
	used := headerCost
	summaryCostOf := make([]int, cutoff)
	for i := 0; i < cutoff; i++ {
		if len(ranked[i].Symbols) == 0 {
			ranked[i].DetailLevel = 0
			continue
		}
		summaryCost := summaryCost(ranked[i])
		if used+summaryCost <= budgetBytes {
			ranked[i].DetailLevel = 1
			used += summaryCost
			summaryCostOf[i] = summaryCost
		}
	}

	// Depth pass: promote summaries to full symbol blocks where the cost
	// fits, reclaiming the summary bytes already charged.
	for i := 0; i < cutoff; i++ {
		if ranked[i].DetailLevel != 1 {
			continue
		}
		full := cost(ranked[i].Symbols)
		if used-summaryCostOf[i]+full <= budgetBytes {
			ranked[i].DetailLevel = 2
			used += full - summaryCostOf[i]
		}
	}
	return ranked
}

// summaryCost estimates the rendered bytes of one level-1 counts line, never
// exceeding the file's full-symbol cost (a file cheaper in full stays cheaper
// in summary).
func summaryCost(rf RankedFile) int {
	counts := len(kindCounts(rf.Symbols))
	return counts*8 + len(path.Base(rf.Path)) + 24
}

// kindCounts tallies symbols per kind keyword.
func kindCounts(symbols []analyze.Symbol) map[string]int {
	counts := make(map[string]int, 4)
	for _, symbol := range symbols {
		counts[symbol.Kind]++
	}
	return counts
}

// intentTerms lowercases an intent string into ranking terms: words of at
// least three runes minus a stopword set, expanded with identifier sub-tokens.
func intentTerms(intent string) []string {
	return ExpandTerms(intent, 3, CommonStopwords)
}
