package scan

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// signalsFile is the on-disk shape shared by source_signals.yaml and
// store_signals.yaml: signals: { "import/prefix": "Label", ...}.
type signalsFile struct {
	Signals map[string]string `yaml:"signals"`
}

// loadSignalTable parses the embedded defaults and overlays the user file
// (~/.config/pan/<filename>) when present; user keys win on collision.
// All failures degrade silently to defaults — a missing or garbled override
// never panics, and unknown imports are simply not recognized downstream.
// Callers invoke this once per scan and reuse the returned map, so no
// package-level cache state is needed.
func loadSignalTable(embedded []byte, filename string) map[string]string {
	var defaults signalsFile
	if err := yaml.Unmarshal(embedded, &defaults); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(defaults.Signals)+8)
	for k, v := range defaults.Signals {
		out[k] = v
	}
	if path, ok := userSignalsPath(filename); ok {
		if data, err := os.ReadFile(path); err == nil {
			var overrides signalsFile
			if err := yaml.Unmarshal(data, &overrides); err == nil {
				for k, v := range overrides.Signals {
					out[k] = v
				}
			}
		}
	}
	return out
}

// userSignalsPath returns the XDG-aware override path for the given basename
// and whether a HOME-like directory could be resolved.
func userSignalsPath(filename string) (string, bool) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "pan", filename), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".config", "pan", filename), true
}
