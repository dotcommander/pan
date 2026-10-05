# Inspect a repository

Use this guide to get a bounded starting picture of a repository, then narrow the next read or query from evidence rather than a recursive walk.

## Start with an overview

**Prerequisites:** this source checkout requires Go 1.25 and `just`. `just build` writes the project-local `./pan` binary. On a normal first analysis, Pan creates its user configuration file from embedded defaults when that file does not exist.

```bash
just build
./pan --repo . --format json scan overview
```

The second command emits a `pan/v1` JSON envelope. Its `result` contains `files`, `symbols`, `edges`, language counts, and other snapshot-derived counts. Its `analysis` object contains the completeness decision, limits, and skipped paths. Pan obtains those values from one bounded source snapshot; it does not run the target repository.

Before an absence claim, run this source-checked, unexecuted variation:

```bash
./pan --repo . scan doctor
```

`scan doctor` reports analyzer health. It uses a read-only configuration load, so it can check configuration state without creating a missing configuration file.

## Choose the next query

| You know… | Use… | What it returns |
| --- | --- | --- |
| Only the task or goal | `./pan --repo . context brief "describe the task"` | A task-oriented repository summary. |
| A bounded implementation packet | `./pan --repo . context task "describe the task" --tokens 4096` | Distinct matched goal terms, selected targets, and recorded omissions. |
| A symbol to trace | `./pan --repo . flow calls SymbolName --depth 2` | Bounded call-edge evidence; use an exact `symbol:` handle when names collide. |
| Graph reference uncertainty | `./pan --repo . --format json scan graph` | Resolved edges and bounded `unresolved` reference reasons. |
| A route to trace | `./pan --repo . flow endpoint /route` | Route-to-handler-to-test evidence. |
| A file whose change impact matters | `./pan --repo . context impact path/to/file.go` | Evidence-backed blast radius for that file. |
| A review starting point | `./pan --repo . scan risks` | A risk-ranked review queue. |
| A change's symbol-level shape | `./pan --repo . scan diff [REV]` | Per-symbol added/removed/body/signature/moved overlay over the working tree or a git range. |

These examples are source-checked and unexecuted in this documentation update. `flow calls` requires a positive `--depth`; the default is `2`. `context brief` accepts an approximate byte `--budget` and defaults to the `compact` detail projection. Use `--detail evidence` when a consumer needs the fuller projection, or `--detail paths` for a lightweight path list.

## Interpret incomplete evidence

A bounded snapshot can omit files because a configured size, node, or other limit applies. It can also exclude configured paths. The useful test is not “did Pan return nothing?” but “did the relevant snapshot cover the thing I need?”

| Symptom | Cause | Solution | Prevention |
| --- | --- | --- | --- |
| A symbol or file is absent from a result. | The snapshot may be incomplete or the path may be excluded. | Read the command's `analysis` fields and run `scan doctor` when coverage is unclear. | Treat absence as a conclusion only after coverage supports it. |
| A command needs more detail than the default result shows. | Some task-oriented commands project compact output by default. | Use that command's `--detail evidence` option. | Inspect `pan <command> --help` before scripting against a result shape. |

Go call edges carry an exact `target` only when the declaration was captured and type-resolved. Interface dispatch and calls without an in-snapshot target remain lexical; a name shared by multiple declarations is not attributed to all of them. Use `context find SymbolName` to obtain an exact `symbol:` handle for `flow calls` or `flow impact` when Pan reports ambiguity.

For Python and TypeScript-family references, only uniquely matched, supported named imports become dependency edges. Unbound, shadowed, missing, or colliding candidates are listed by `scan graph` under `unresolved` with `unresolved_count` and a truncation record when the bounded list overflows. An unresolved reference is not evidence that its target is absent. `analysis.complete` remains a separate statement about discovery coverage.

`context task` tries feasible candidates for distinct matched goal terms before filling remaining slots by rank. When one target cannot fit, it tries later targets; source excerpts can be omitted with explicit truncation records. `budget.used_tokens` charges the finalized JSON packet by Pan's byte-based estimate (`ceil(encoded bytes / 4)`), not an exact model tokenizer.

`pan <command> --help` is the supported way to inspect a command's current arguments and side effects.
