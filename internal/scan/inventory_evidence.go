package scan

import (
	"regexp"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// Boundary roles an owner can play for its boundary operations.
const (
	roleConstructor = "constructor"
	roleReader      = "reader"
	roleWriter      = "writer"
)

// inventoryOp extracts the leading call expression from one evidence line,
// for example "sql.Open" from `db, err := sql.Open("postgres", dsn)`.
var inventoryOp = regexp.MustCompile(`\b([A-Za-z_][\w.]*)\(`)

// inventoryRoles returns the call-op suffix to boundary-role mapping.
// Unlisted ops are skipped: an unclassified operation is absent evidence,
// not a guess.
func inventoryRoles() map[string][]string {
	return map[string][]string{
		// Constructors open or build the boundary client.
		"Open": {roleConstructor}, "Connect": {roleConstructor}, "ConnectConfig": {roleConstructor},
		"New": {roleConstructor}, "NewPool": {roleConstructor}, "NewWithConfig": {roleConstructor},
		"NewClient": {roleConstructor}, "NewRequest": {roleConstructor}, "NewServeMux": {roleConstructor},
		// Readers consume boundary state without changing it.
		"Get": {roleReader}, "Head": {roleReader}, "Stat": {roleReader}, "Read": {roleReader},
		"ReadFile": {roleReader}, "Query": {roleReader}, "QueryContext": {roleReader},
		"QueryRow": {roleReader}, "QueryRowContext": {roleReader}, "Select": {roleReader},
		"Decode": {roleReader}, "Unmarshal": {roleReader}, "Ping": {roleReader},
		// Writers mutate boundary state.
		"Post": {roleWriter}, "Put": {roleWriter}, "Patch": {roleWriter}, "Delete": {roleWriter},
		"Exec": {roleWriter}, "ExecContext": {roleWriter}, "Begin": {roleWriter}, "BeginTx": {roleWriter},
		"Commit": {roleWriter}, "Rollback": {roleWriter}, "CopyFrom": {roleWriter}, "SendBatch": {roleWriter},
		"Write": {roleWriter}, "WriteFile": {roleWriter}, "Create": {roleWriter}, "CreateTemp": {roleWriter},
		"Remove": {roleWriter}, "RemoveAll": {roleWriter}, "Rename": {roleWriter}, "MkdirAll": {roleWriter},
		"Encode": {roleWriter}, "Marshal": {roleWriter}, "Do": {roleWriter},
	}
}

// inventoryOwnerMap accumulates the classified boundary operations from an
// effects packet into per-file owners.
func inventoryOwnerMap(effects EffectsReport, kindSet map[string]bool, roles map[string][]string) map[string]*InventoryOwner {
	owners := map[string]*InventoryOwner{}
	for _, file := range effects.Files {
		for _, effect := range file.Effects {
			if !kindSet[effect.Kind] {
				continue
			}
			role, op, ok := classifyInventoryEvidence(effect, roles)
			if !ok {
				continue
			}
			owner := owners[file.Path]
			if owner == nil {
				owner = &InventoryOwner{Path: file.Path}
				owners[file.Path] = owner
			}
			for _, r := range role {
				if !slices.Contains(owner.Roles, r) {
					owner.Roles = append(owner.Roles, r)
				}
			}
			owner.Evidence = append(owner.Evidence, InventoryEvidence{
				Role: strings.Join(role, "+"), Kind: effect.Kind, Op: op, Line: effect.Line,
				Confidence: analyze.ConfidenceLexical,
			})
		}
	}
	return owners
}

// finalizeInventoryOwners sorts and caps each owner's evidence, returns the
// owners sorted by path, and applies the deterministic owner cap.
func finalizeInventoryOwners(owners map[string]*InventoryOwner, truncations []Truncation) ([]InventoryOwner, []Truncation) {
	var out []InventoryOwner
	for _, owner := range owners {
		slices.Sort(owner.Roles)
		slices.SortFunc(owner.Evidence, func(a, b InventoryEvidence) int {
			if a.Line != b.Line {
				return a.Line - b.Line
			}
			if a.Role != b.Role {
				return strings.Compare(a.Role, b.Role)
			}
			return strings.Compare(a.Op, b.Op)
		})
		if len(owner.Evidence) > maxInventoryEvidence {
			truncations = append(truncations, Truncation{
				Field: "owners[" + owner.Path + "].evidence", Shown: maxInventoryEvidence, Total: len(owner.Evidence), Reason: "inventory per-owner cap",
			})
			owner.Evidence = owner.Evidence[:maxInventoryEvidence:maxInventoryEvidence]
		}
		out = append(out, *owner)
	}
	slices.SortFunc(out, func(a, b InventoryOwner) int {
		return strings.Compare(a.Path, b.Path)
	})
	if total := len(out); total > maxInventoryOwners {
		truncations = append(truncations, Truncation{
			Field: "owners", Shown: maxInventoryOwners, Total: total, Reason: "inventory owner cap",
		})
		out = out[:maxInventoryOwners:maxInventoryOwners]
	}
	return out, truncations
}

// classifyInventoryEvidence prefers the captured call identity. Display evidence
// need not contain source text; legacy lexical effects still use that fallback.
func classifyInventoryEvidence(effect Effect, roles map[string][]string) (matched []string, op string, ok bool) {
	op = effect.Op
	if op == "" {
		match := inventoryOp.FindStringSubmatch(effect.Evidence)
		if match == nil {
			return nil, "", false
		}
		op = match[1]
	}
	suffix := op
	if idx := strings.LastIndexByte(op, '.'); idx >= 0 {
		suffix = op[idx+1:]
	}
	matched, ok = roles[suffix]
	return matched, op, ok
}
