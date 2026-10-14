# Import existing tool reports

`pan review import <ast-grep|jscpd|scip> INPUT` imports existing evidence. Use
`-` for stdin. Pan does not run the producer, read reported source paths, or
require a repository directory. For example:

```sh
pan --format json review import ast-grep matches.json
pan --format json review import jscpd report.json
pan --format json --artifact imported-index.json review import scip index.scip
cat matches.ndjson | pan --format json review import ast-grep -
```

The existing `--format text` and `--artifact` behavior applies. JSON uses the
`pan/v1` envelope with a `tool-report/v1` result. `analysis.complete` and
`result.import.complete` mean the supplied input was fully parsed. They say
nothing about repository coverage. Source coverage, freshness and exclusions
remain `unknown`; there is no snapshot or verified current-source identity.
Malformed input produces an error before any report is published.

## Supported inputs and evidence

- **ast-grep:** CLI run/scan JSON arrays and `--json=stream` object-per-line
  output, following the upstream [JSON printer schema](https://github.com/ast-grep/ast-grep/blob/main/crates/cli/src/print/json_print.rs).
  Matches, rule fields, captures, labels, replacements and additive fields are
  retained. Line and Unicode scalar columns stay zero-based; byte offsets stay
  half-open. The CLI report does not establish a producer version or run query.
- **jscpd:** Rust v5 JSON reports with `statistics` and `duplicates`, following
  the pinned [reporter schema](https://github.com/kucherenko/jscpd/blob/3cc4758c8b2cdb9fb05d19776e4e7d09d94c7988/rust/crates/cpd-reporter/src/json_reporter.rs).
  Both pair locations, fragments, sizes, optional nodes/blame/similarity/method,
  summary/history and additive metadata are retained. Native coordinate units
  and base remain unverified. Legacy `statistic` reports fail actionably.
  Clone evidence is approximate and grants no deletion or refactoring authority.
- **SCIP:** native protobuf indexes using the official codec at
  `fcb3cb3c679f`, following the pinned [SCIP schema](https://github.com/scip-code/scip/blob/fcb3cb3c679fe2da18141550f864b9e67afab297/scip.proto).
  All known index fields are projected, including metadata, documents, symbols,
  relationships, diagnostics, signatures and external symbols. Per-document
  position encoding and numeric role combinations retain their upstream meaning.
  Local symbols remain document-scoped. Typed ranges take precedence; coexisting
  legacy ranges must agree. Unknown enums, role bits and wire fields are declared
  uninterpreted. `original_protobuf_base64` preserves every original byte,
  including unknown fields that a JSON projection alone cannot preserve.

Raw JSON evidence preserves numeric lexemes, including large integers and
exponents. Whitespace is compacted; no source content is synthesized. Evidence
retains upstream order. `views` are deterministic indexes sorted by path, range,
symbol and original ordinal; they add no findings or confidence. Their ordinals
refer to original matches/pairs or SCIP document occurrences. Exact input bytes,
SHA-256 and schema reference accompany each report. Producer/version are null
when unavailable, even if the importer knows a schema version.

## Bounds and safety

Defaults are 64 MiB input, 256 MiB rendered output, nesting depth 64, 100,000
aggregate decoded values/wire records, 10,000 SCIP documents, 8 MiB per string,
and 16,384 bytes per path. Aggregate limits include metadata and nested fields,
so a report can reach them before reaching that many matches. Input is rejected
as a whole on malformed UTF-8, duplicate JSON keys, trailing content, ambiguous
protobuf singular fields/oneofs, inconsistent ranges or exceeded bounds.
SCIP metadata must appear first and once. Unpaired UTF-16 escapes in JSON
strings or keys are rejected before decoding; valid surrogate pairs and literal
replacement characters retain their identities. Output escaping and indentation
are accounted for before expanded JSON intermediates or rendered output are
allocated; oversized output is rejected as a whole.

Absolute and Windows paths in ast-grep/jscpd reports are inert identities.
Traversal segments and controls are rejected across both separators; SCIP
document paths must be canonical relative slash paths. Embedded instructions
are data. No report path causes filesystem access or execution.

Acceptance fixtures exercise importer behavior against documented shapes and
independent wire values. They do not establish that an upstream engine was run,
that its report is current, or that it covered a whole repository. The completed
`pan context pack` flow remains a separate way to normalize captured repository
content.

For supplied handwritten inputs, including native SCIP, and runnable file/stdin/artifact examples without producer tools, see [tool-input examples](../examples/tool-inputs/README.md).
