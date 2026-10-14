package scan

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// Inventory bounds: owners and per-owner evidence mirror the other bounded
// packets; path lists cap independently so one pathological tree cannot
// inflate the report.
const (
	maxInventoryOwners    = 100
	maxInventoryEvidence  = 12
	maxInventoryPathLists = 50
	maxInventoryTests     = 100
)

// inventoryCaveat is stamped on every inventory report: role classification
// is heuristic outside Go AST calls, so interface indirection and dynamic
// dispatch are invisible.
// termJSON is the serialization search term and its boundary alias.
const termJSON = "json"

const inventoryCaveat = "Owners and roles are static: pan matches parsed Go calls and bounded source shapes in every parsed language. Interface indirection, ORMs, and dynamic dispatch can hide owners; verify before relying on the inventory."

// InventoryReport is the deterministic owner inventory for one trust
// boundary: which files own boundary operations and in which roles, plus
// the migrations, tests, and docs that plausibly belong to the boundary.
type InventoryReport struct {
	SchemaVersion string           `json:"schema_version"`
	Boundary      string           `json:"boundary"`
	EffectKinds   []string         `json:"effect_kinds"`
	Caveat        string           `json:"caveat"`
	Owners        []InventoryOwner `json:"owners"`
	Migrations    []string         `json:"migrations,omitempty"`
	Tests         []string         `json:"tests,omitempty"`
	Docs          []string         `json:"docs,omitempty"`
	Truncations   []Truncation     `json:"truncations,omitempty"`
}

// InventoryOwner is one file that performs boundary operations, with the
// deduplicated roles it plays and the classified evidence lines.
type InventoryOwner struct {
	Path     string              `json:"path"`
	Roles    []string            `json:"roles"`
	Evidence []InventoryEvidence `json:"evidence"`
}

// InventoryEvidence is one classified boundary operation.
type InventoryEvidence struct {
	Role       string `json:"role"`
	Kind       string `json:"kind"`
	Op         string `json:"op"`
	Line       int    `json:"line"`
	Confidence string `json:"confidence"`
}

// inventoryBoundary is one canonical boundary with its effect-kind aliases.
type inventoryBoundary struct {
	Canonical string
	Kinds     []string
	// Terms are the lowercase path/content terms matching boundary assets.
	Terms []string
}

// inventoryBoundaries returns every accepted boundary spelling mapped to
// its canonical form, built per inventory run so the package owns no
// mutable global tables. Effect kinds must exist in the effects pattern
// table; the shared prefixes keep a repository-wide alias (filesystem,
// concurrency) from fabricating evidence pan cannot extract.
func inventoryBoundaries() map[string]inventoryBoundary {
	pg := inventoryBoundary{Canonical: "Postgres", Kinds: []string{kindDatabase}, Terms: []string{"postgres", "postgresql", "pgx", "sql", "database", "migration", "schema"}}
	fs := inventoryBoundary{Canonical: boundaryFilesystem, Kinds: []string{kindFilesystemWrite, kindFilesystemRead}, Terms: []string{"fs", "file", "storage", "data"}}
	web := inventoryBoundary{Canonical: "HTTP", Kinds: []string{kindHTTP}, Terms: []string{kindHTTP, "api", "route", "endpoint"}}
	proc := inventoryBoundary{Canonical: "Subprocess", Kinds: []string{kindSubprocess}, Terms: []string{"exec", kindSubprocess, "command"}}
	exit := inventoryBoundary{Canonical: "Exit", Kinds: []string{kindProcessExit}, Terms: []string{"exit"}}
	secret := inventoryBoundary{Canonical: "Secret", Kinds: []string{kindSecret}, Terms: []string{kindSecret, termCredential, "password", "token"}}
	crypto := inventoryBoundary{Canonical: "Crypto", Kinds: []string{kindCrypto}, Terms: []string{kindCrypto, "cipher", "hash"}}
	serial := inventoryBoundary{Canonical: "Serialization", Kinds: []string{kindSerialization}, Terms: []string{termJSON, "yaml", "encode", "decode"}}
	timing := inventoryBoundary{Canonical: "Time", Kinds: []string{kindTime}, Terms: []string{"time", "clock", "schedule"}}
	random := inventoryBoundary{Canonical: "Randomness", Kinds: []string{kindRandomness}, Terms: []string{"random", "rand", "seed"}}
	conc := inventoryBoundary{Canonical: "Concurrency", Kinds: []string{kindGoroutine, kindContextBackground}, Terms: []string{kindGoroutine, "worker", "async"}}
	stream := inventoryBoundary{Canonical: "UnboundedRead", Kinds: []string{kindUnboundedRead}, Terms: []string{"read", "stream"}}
	return map[string]inventoryBoundary{
		"postgres": pg, "postgresql": pg, "pgx": pg, "sql": pg, "database": pg, "db": pg,
		"fs": fs, "filesystem": fs, "file": fs, "storage": fs,
		kindHTTP: web, "api": web, "web": web,
		kindSubprocess: proc, "process": proc, "exec": proc,
		"exit":     exit,
		kindSecret: secret, "secrets": secret, termCredential: secret,
		kindCrypto: crypto, "cryptography": crypto,
		"serialization": serial, termJSON: serial, "yaml": serial,
		"time": timing, "clock": timing,
		"random": random, "randomness": random, "rand": random,
		kindGoroutine: conc, "concurrency": conc,
		"unbounded-read": stream, "read": stream,
	}
}

// Inventory derives the owner inventory for one trust boundary from the
// snapshot: files owning boundary operations with constructor/reader/writer
// roles, plus boundary-adjacent migrations, tests, and docs. All evidence
// is static matching over parsed source files the effects packet already
// inspects.
// top > 0 caps the owner list; truncations keep totals observable.
func Inventory(ctx context.Context, snap analyze.Snapshot, boundary string, top int) (InventoryReport, error) {
	boundaries := inventoryBoundaries()
	canonical, kinds, err := normalizeInventoryBoundary(boundary, boundaries)
	if err != nil {
		return InventoryReport{}, err
	}
	kindSet := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		kindSet[kind] = true
	}

	effects, err := Effects(ctx, snap, 0)
	if err != nil {
		return InventoryReport{}, err
	}

	report := InventoryReport{
		SchemaVersion: snap.SchemaVersion,
		Boundary:      canonical,
		EffectKinds:   kinds,
		Caveat:        inventoryCaveat,
		Owners:        []InventoryOwner{},
		Truncations:   []Truncation{},
	}
	ownerMap := inventoryOwnerMap(effects, kindSet, inventoryRoles())
	report.Owners, report.Truncations = finalizeInventoryOwners(ownerMap, report.Truncations)
	if top > 0 && len(report.Owners) > top {
		report.Truncations = append(report.Truncations, Truncation{
			Field: "owners", Shown: top, Total: len(report.Owners), Reason: "truncated by --top",
		})
		report.Owners = report.Owners[:top]
	}

	terms := inventoryTerms(boundary, kinds, boundaries)
	report.Migrations, report.Truncations = inventoryAssetPaths(snap.Root, snap.Files, terms, true, report.Truncations)
	report.Docs, report.Truncations = inventoryAssetPaths(snap.Root, snap.Files, terms, false, report.Truncations)
	report.Tests, report.Truncations = inventoryTests(snap, report.Owners, report.Truncations)
	return report, nil
}

// normalizeInventoryBoundary resolves one user-facing boundary spelling to
// its canonical name and effect kinds. Unknown boundaries are errors that
// name the accepted spellings, never silent empty reports.
func normalizeInventoryBoundary(boundary string, boundaries map[string]inventoryBoundary) (string, []string, error) {
	key := strings.ToLower(strings.TrimSpace(boundary))
	if entry, ok := boundaries[key]; ok {
		return entry.Canonical, entry.Kinds, nil
	}
	if slices.Contains(EffectKinds(), key) {
		return inventoryKindCanonical(key), []string{key}, nil
	}
	known := make([]string, 0, len(boundaries))
	for name := range boundaries {
		known = append(known, name)
	}
	slices.Sort(known)
	return "", nil, fmt.Errorf("unknown boundary %q; accepted boundaries: %s", boundary, strings.Join(known, ", "))
}

// inventoryTerms returns the asset-matching terms for one boundary
// spelling: the alias table's terms when the spelling is a known alias,
// otherwise terms derived from the effect-kind name segments.
func inventoryTerms(boundary string, kinds []string, boundaries map[string]inventoryBoundary) []string {
	if terms := boundaries[strings.ToLower(strings.TrimSpace(boundary))].Terms; terms != nil {
		return terms
	}
	return kindTerms(kinds)
}

// inventoryKindCanonical renders one effect-kind name as a canonical
// boundary label by upper-casing its first letter only.
func inventoryKindCanonical(kind string) string {
	if kind == "" {
		return kind
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// kindTerms derives lowercase path terms from effect-kind names, splitting
// on hyphens so "filesystem-write" yields "filesystem" and "write".
func kindTerms(kinds []string) []string {
	terms := make([]string, 0, len(kinds)*2)
	for _, kind := range kinds {
		for _, segment := range strings.Split(kind, "-") {
			if segment != "" {
				terms = append(terms, segment)
			}
		}
	}
	return terms
}

// inventoryAssetPaths lists snapshot paths plausibly belonging to the
// boundary: migrations match by path shape, docs by path or bounded content.
func inventoryAssetPaths(root string, files []analyze.File, terms []string, migrations bool, truncations []Truncation) ([]string, []Truncation) {
	var out []string
	for _, file := range files {
		low := strings.ToLower(file.Path)
		if isTestPath(file.Path) {
			continue
		}
		if migrations {
			if !inventoryPathLooksLikeMigration(low) || !pathHasAnyTerm(low, terms) {
				continue
			}
			out = append(out, file.Path)
			continue
		}
		if !strings.HasSuffix(low, ".md") && !strings.HasSuffix(low, ".mdx") && !strings.HasSuffix(low, ".txt") {
			continue
		}
		if pathHasAnyTerm(low, terms) || inventoryDocMentionsTerms(path.Join(root, filepathFromSlash(file.Path)), terms) {
			out = append(out, file.Path)
		}
	}
	slices.Sort(out)
	limit := maxInventoryPathLists
	field := "docs"
	if migrations {
		field = "migrations"
	}
	if len(out) > limit {
		truncations = append(truncations, Truncation{
			Field: field, Shown: limit, Total: len(out), Reason: "inventory path-list cap",
		})
		out = out[:limit:limit]
	}
	return out, truncations
}

// inventoryPathLooksLikeMigration reports whether a lowercase path carries
// migration or schema evidence or is a SQL file.
func inventoryPathLooksLikeMigration(low string) bool {
	return strings.Contains(low, "migration") || strings.Contains(low, "schema") || strings.HasSuffix(low, ".sql")
}

// pathHasAnyTerm reports whether a lowercase path contains any term.
func pathHasAnyTerm(low string, terms []string) bool {
	for _, term := range terms {
		if term != "" && strings.Contains(low, term) {
			return true
		}
	}
	return false
}

// inventoryDocMentionsTerms reads one documentation file (bounded by the
// shared scan line bound) and reports whether it mentions any boundary
// term. Read failures count as a miss: docs evidence degrades to
// path-only matching.
func inventoryDocMentionsTerms(absPath string, terms []string) bool {
	lines, _, err := readLines(context.Background(), absPath)
	if err != nil {
		return false
	}
	for _, line := range lines {
		low := strings.ToLower(line.text)
		if pathHasAnyTerm(low, terms) {
			return true
		}
	}
	return false
}

// inventoryTests lists the sibling test files for each owner, deduplicated
// and sorted, capped with a truncation record.
func inventoryTests(snap analyze.Snapshot, owners []InventoryOwner, truncations []Truncation) ([]string, []Truncation) {
	present := make(map[string]bool, len(snap.Files))
	for _, file := range snap.Files {
		present[file.Path] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, owner := range owners {
		test := strings.TrimSuffix(owner.Path, path.Ext(owner.Path)) + "_test.go"
		if !present[test] || seen[test] {
			continue
		}
		seen[test] = true
		out = append(out, test)
	}
	slices.Sort(out)
	if len(out) > maxInventoryTests {
		truncations = append(truncations, Truncation{
			Field: "tests", Shown: maxInventoryTests, Total: len(out), Reason: "inventory test cap",
		})
		out = out[:maxInventoryTests:maxInventoryTests]
	}
	return out, truncations
}
