# Supplied tool-input examples

Run context packing and all three report importers without installing or executing Repomix, ast-grep, jscpd or an indexer. These small **handwritten illustrative inputs** follow supported formats; they are not captured producer results or evidence about this repository. Their embedded paths are inert identifiers and need not exist.

Requirements: Python 3.8+ (standard library only) and an explicitly selected, already-built Pan binary that includes `context pack` and `review import`. Select the checkout binary you intend to exercise; an installed binary may have different commands. The runner does not build, download, install or search `PATH` for Pan.

From the repository root, after obtaining the intended binary:

```sh
python3 examples/tool-inputs/run.py --pan "$PWD/pan"
# Alternatively, pass an explicit binary through the environment:
PAN_BIN="$PWD/pan" python3 examples/tool-inputs/run.py
```

The runner invokes only that Pan binary. It runs each supplied input through file, stdin and `--artifact` routes, checks meaningful fields and compares the three outputs byte-for-byte. It prints a fresh output directory under `.work/tool-inputs-demo/run-*` and retains all twelve JSON outputs. Use `--output-parent /explicit/output/directory` to choose another parent; each run creates a unique child and performs no cleanup. It runs Pan from that child, outside any source inventory, without `--repo`. It reads only the four supplied fixtures and selected binary; Pan does not open embedded report paths.

## Individual commands

Set `PAN_BIN` to the absolute path of your intended binary. Create a fresh output directory for these commands; the shell examples below use the existing `mktemp` utility and do not remove it afterward.

```sh
PAN_BIN="$PWD/pan"
OUT=$(mktemp -d)
"$PAN_BIN" context pack examples/tool-inputs/repomix.json > "$OUT/pack-file.json"
"$PAN_BIN" context pack - < examples/tool-inputs/repomix.json > "$OUT/pack-stdin.json"
"$PAN_BIN" --artifact "$OUT/pack-artifact.json" context pack examples/tool-inputs/repomix.json

"$PAN_BIN" --format json review import ast-grep examples/tool-inputs/ast-grep.json > "$OUT/ast-file.json"
"$PAN_BIN" --format json review import ast-grep - < examples/tool-inputs/ast-grep.json > "$OUT/ast-stdin.json"
"$PAN_BIN" --artifact "$OUT/ast-artifact.json" --format json review import ast-grep examples/tool-inputs/ast-grep.json

"$PAN_BIN" --format json review import jscpd examples/tool-inputs/jscpd.json > "$OUT/clones-file.json"
"$PAN_BIN" --format json review import jscpd - < examples/tool-inputs/jscpd.json > "$OUT/clones-stdin.json"
"$PAN_BIN" --artifact "$OUT/clones-artifact.json" --format json review import jscpd examples/tool-inputs/jscpd.json

"$PAN_BIN" --format json review import scip examples/tool-inputs/index.scip > "$OUT/scip-file.json"
"$PAN_BIN" --format json review import scip - < examples/tool-inputs/index.scip > "$OUT/scip-stdin.json"
"$PAN_BIN" --artifact "$OUT/scip-artifact.json" --format json review import scip examples/tool-inputs/index.scip
```

`--artifact` writes the result to the named file with no result on stdout. Keep outputs separate from inputs. The Python runner provides portable directory creation and field assertions; these shell commands illustrate the same command routes.

## Inputs and expected evidence

| Input | Handwritten content | Expected result |
| --- | --- | --- |
| `repomix.json` | Repomix-style JSON envelope with three files, including an empty file and a JSON body | Direct `pan-pack/v1`; all three paths retained once in sorted order; body compacted to `{"count":9007199254740993,"huge":1e400}` without numeric changes |
| `ast-grep.json` | One array match for `foo("🚀")`, with an additive metadata integer | `pan/v1` containing `tool-report/v1`; scalar-column range `[0,0,0,8]`, byte offsets `[0,11]`, exact integer `9007199254740993` |
| `jscpd.json` | Rust v5 `statistics`/`duplicates` envelope with one illustrative clone pair | Both file locations and fragment retained; first range `[1,0,2,6]`; upstream coordinate units/base remain unverified |
| `index.scip` | Native protobuf encoded manually from documented wire tags | One UTF-16 document occurrence, range `[0,1,0,3]`, local symbol `local 0`, exact role value 137; original bytes recoverable from base64 |

The SCIP fixture follows the [pinned SCIP schema](https://github.com/scip-code/scip/blob/fcb3cb3c679fe2da18141550f864b9e67afab297/scip.proto), without invoking a producer or generated codec. Index field 1 contains metadata (tool_info field 2, project_root field 3, text_document_encoding field 4). Index field 2 contains a document (relative_path 1, occurrences 2, language 4, text 5, position_encoding 6). Its occurrence uses packed legacy range field 1, symbol field 2 and symbol_roles field 3; unknown varint field 99 carries 7. The document text is `é🚀`; encoding value 2 means UTF-16 code units. Role 137 combines definition (1), read access (8) and an unknown bit (128). The unknown bit and field remain uninterpreted; exact original bytes preserve them. `illustrative-indexer` / `0.0-example` is fabricated fixture metadata, not an observed installed producer.

Successful parsing says the **supplied input** was imported completely. Source coverage, freshness and exclusions remain unknown. Clone reports grant no deletion/refactoring authority. Pack normalization declares prior compression/loss unknown, does not establish original source/line identity, and may make a tiny fixture larger because of its provenance envelope. No example promises size or token savings.

For full contracts and limits, see [context packing](../../docs/inspect-a-repository.md#compact-a-repomix-capture) and [report imports](../../docs/import-tool-reports.md).
