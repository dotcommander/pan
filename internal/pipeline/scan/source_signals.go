package scan

import _ "embed"

//go:embed data/source_signals.yaml
var embeddedSourceSignalsYAML []byte

// sourceSignals returns the import-prefix → read-only external source-label
// table backing detectSources (HTTP, message queues, encodings). Persistent
// datastores are detected separately via store_signals.yaml. The table is
// parsed from the embedded defaults overlaid with
// ~/.config/Pan/source_signals.yaml on each call; callers hoist the
// call out of loops and reuse the result.
func sourceSignals() map[string]string {
	return loadSignalTable(embeddedSourceSignalsYAML, "source_signals.yaml")
}
