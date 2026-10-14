# Independent report fixtures

These fixtures are hand-written from the upstream schemas listed below, rather
than emitted by Pan's decoder or an upstream executable. Locations name
nonexistent files deliberately: importing a report must not read source files.

- `ast-array.json`: CLI MatchJSON/scan fields from ast-grep
  `crates/cli/src/print/json_print.rs` (the primary schema linked in the result).
  Includes non-ASCII text, Unicode scalar columns, byte offsets, captures,
  replacement, labels and additive metadata with exact numeric lexemes.
- `jscpd-v5.json`: Rust JSON reporter at upstream commit
  `3cc4758c8b2cdb9fb05d19776e4e7d09d94c7988`, with asymmetric clone locations,
  Windows/absolute identities, approximate similarity, nodes/blame and extras.
- SCIP fixtures in `scip_test.go` use a small test-only wire encoder and literal
  documented field numbers from `scip.proto` at
  `fcb3cb3c679fe2da18141550f864b9e67afab297`. They do not call the generated SCIP
  marshal implementation or the importer to construct expected wire data.

The fixtures prove report ingestion and retained evidence. They do not prove a
live upstream engine run, current source identity or repository completeness.
