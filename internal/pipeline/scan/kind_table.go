package scan

import (
	_ "embed"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed data/kind_table.yaml
var embeddedKindTableYAML []byte

// kindRow is one entry in the ordered package-name fragment → Kind table.
// Order matters: inferKind walks the slice top-to-bottom and returns the
// first prefix match. The list-of-pairs shape preserves intentional
// duplicates (e.g. "route" → "Route" AND "Serve") that a map would lose.
type kindRow struct {
	Match string `yaml:"match"`
	Kind  string `yaml:"kind"`
}

type kindTableFile struct {
	Kinds []kindRow `yaml:"kinds"`
}

// kindTableRows returns the ordered match→kind rows, parsing the embedded
// YAML and prepending ~/.config/pan/kind_table.yaml entries when present
// (user entries match first). Callers invoke this once per scan and walk the
// returned rows, so no package-level cache state is needed.
func kindTableRows() []kindRow {
	return loadKindTable()
}

func loadKindTable() []kindRow {
	var defaults kindTableFile
	if err := yaml.Unmarshal(embeddedKindTableYAML, &defaults); err != nil {
		// Bad embedded YAML is a programmer bug; fall through to empty
		// table — inferKind returns "" for unknown kinds, matching the
		// pre-change "no match found" behavior.
		return nil
	}

	// User overlay is PREPENDED so user entries match before defaults.
	// This lets the user override "spec" → "Parse" by adding their own
	// "spec" → "Custom" row at the top of their YAML.
	var rows []kindRow
	if path, ok := userKindTablePath(); ok {
		if data, err := os.ReadFile(path); err == nil {
			var overrides kindTableFile
			if err := yaml.Unmarshal(data, &overrides); err == nil {
				rows = append(rows, overrides.Kinds...)
			}
		}
	}
	rows = append(rows, defaults.Kinds...)
	return rows
}

// userKindTablePath returns the user-override path (XDG-aware) and whether
// a HOME-like directory could be resolved.
func userKindTablePath() (string, bool) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "pan", "kind_table.yaml"), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".config", "pan", "kind_table.yaml"), true
}
