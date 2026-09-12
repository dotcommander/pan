package scan

import _ "embed"

//go:embed data/store_signals.yaml
var embeddedStoreSignalsYAML []byte

// storeSignals returns the import-prefix → datastore-name table backing
// detectStores' persistence-import detection (PostgreSQL, SQLite, Redis, …).
// Read-only external inputs live in source_signals.yaml instead. The table
// is parsed from the embedded defaults overlaid with
// ~/.config/Pan/store_signals.yaml on each call; callers hoist the call
// out of loops and reuse the result.
func storeSignals() map[string]string {
	return loadSignalTable(embeddedStoreSignalsYAML, "store_signals.yaml")
}
