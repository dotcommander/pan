<p align="center">
  <img src="assets/pan-logo.jpeg" alt="Pan logo" width="560">
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go Version"></a>
  <a href="https://github.com/dotcommander/homebrew-tap"><img src="https://img.shields.io/badge/Homebrew-dotcommander%2Ftap-orange?logo=homebrew" alt="Homebrew Tap"></a>
</p>

# Pan

Pan is a local Go CLI for inspecting a repository, tracing code evidence, and running guarded cleanup or improvement workflows.

| Need | Start here |
| --- | --- |
| Give a person or agent focused code context | `context map`, `context find`, or `context brief` |
| Follow execution, dependencies, routes, or pipelines | `flow entry`, `flow calls`, or `flow scan` |
| Inspect repository shape and review priorities | `scan overview`, `scan risks`, or `review report` |
| Plan cleanup without changing files | `clean plan` |
| Evaluate a guarded test or refactor workflow | `improve recommend` or `improve probe` |
| Integrate Pan with another coding agent | `agent schema` |

## Installation

### Homebrew (macOS & Linux)

```bash
brew install dotcommander/tap/pan
```

### Go Install

```bash
go install github.com/dotcommander/pan/cmd/pan@latest
```

### Build from Source

**Prerequisite:** Go 1.25+.

```bash
git clone https://github.com/dotcommander/pan.git
cd pan
just build
```

## First run

Run Pan against the current repository:

```bash
pan --repo . scan overview
```

The second command reports the repository path plus counts such as `files`, `symbols`, and `edges`. It works by building a bounded source-analysis snapshot of the directory selected by `--repo`; it does not execute the target repository's code. If the result says `complete: false`, inspect its listed limits and skipped paths before treating an absent result as conclusive.

To inspect a different repository, change only the selector:

```bash
./bin/pan --repo /path/to/project scan overview
```

`--repo` defaults to `.` and can also be set with `PAN_REPO`. Add `--format json` when the command's normal result envelope should be JSON. Some document-producing flow commands have their own raw-output modes; use their command help before combining those modes with `--format json`.

## Configuration and state

Most commands load Pan's configuration. On a normal first run, Pan seeds the platform user configuration directory at `pan/config.yaml`; `scan doctor` reads configuration without creating a missing file. Inspect or manage that file with:

```bash
./bin/pan config print
./bin/pan config validate
./bin/pan config init
```

`config init` does not overwrite an existing file unless `--force` is supplied. `improve` can use its own YAML settings through `--config`; its default run history location is under the platform user configuration directory at `pan/improve` unless `improve.state_dir` overrides it. Cache commands manage incremental-analysis cache state; use `cache status`, `cache warm`, and `cache clear` deliberately because warming or clearing changes that local state.

## What Pan does not prove

Pan analyzes source; it does not run the target repository's code. Its evidence can be incomplete because of configured bounds, excluded files, generated code, reflection, dynamic dispatch, or parsing and package-loading limits. Treat results as evidence to inspect, not proof that a behavior or dependency cannot exist.

## Capability reference

| Command group | Evidence it provides or action it performs |
| --- | --- |
| `context` | Repository briefs and maps; symbol, file-impact, route, and language-server queries. |
| `flow` | Entry points, imports, call and impact evidence, pipeline specifications, rendered pipeline documents, validation, review, and local serving. |
| `scan` | Repository, file, symbol, risk, public-surface, effect, hygiene, change, orphan, inventory, and analyzer-health reports. |
| `review` | Briefs, reports, risk and effect packets, outcome-ledger evaluation, and output-surface inventory. |

| `clean` | Cleanup plans, findings, completeness gaps, and copyable proposed commands; application is separately guarded. |
| `improve` | Recommendations, coverage census, proposal probing, guarded test preparation and refactoring, and history statistics. |
| `agent` | Sequential JSONL and JSON-RPC-over-NDJSON stdio protocols, plus their machine-readable schema. |
| `cache` and `config` | Incremental-analysis cache lifecycle and configuration initialization, validation, and display. |

`context brief` reports its variable-content byte usage, analysis coverage,
rank evidence, symbol families, and suggested inspection commands. `scan risks`
defaults to production source, exposes structured score components and coverage,
and accepts repeatable `--include-class` selectors. Its priority is a deterministic
review-order signal; `score` remains an equal-valued compatibility alias.

The task-oriented JSON commands `context brief`, `brief`, `scan risks`,
`review risks`, and `clean plan` accept `--detail compact|evidence` (with `paths`
supported where applicable); `compact` is the default.
Compact output keeps the decision-making fields while omitting verbose projections,
`evidence` preserves the full report, and `paths` is intended for lightweight
scripting. JSON results always include `result.detail` and a sorted
`result.omitted_fields`; projection omissions are separate from any
`budget_info.truncations`. For path-only consumers, read `.result.paths[]`.
Compact risk output retains per-file lane assignments but omits the
duplicated top-level lane catalog; use `evidence` when that catalog is needed.
Compact clean plan output retains candidate buckets, health scores, and summary counts
while omitting compliant inventory (`all_files`); use `evidence` when the complete disposition catalog is needed.
Evidence risk output pairs each source-pattern location with its trimmed matched
line, capped at three matches and 200 runes per line; compact output omits both.
`context explain FILE` reserves its token budget for the requested file. Its JSON
result reports the total `symbol_count`, includes analyzed `symbols` when full
detail fits, and otherwise returns an explicit `omitted_reason`.

Run `./bin/pan <command> --help` for the arguments, outputs, and side effects of one command. `version` prints the binary and analysis-schema versions.

## Guarded operations

Read the plan before enabling a write:

```bash
./bin/pan --repo /path/to/project clean plan
./bin/pan --repo /path/to/project clean apply
```

`clean apply` is a dry run unless `--confirm` is present. With `--confirm`, Pan creates a `tar.gz` backup of every touched existing path in `.work/archive/`, applies the plan, and writes a JSON manifest beside the backup. It refuses to rely on a shell to execute cleanup steps, and untracked paths do not receive Git-index operations.

`improve recommend` and `improve stats` are read-only. `improve probe` validates a proposal without applying it. Guarded preparation and refactoring use an isolated copy by default; `--live` is the explicit mode that can commit a validated result. Provider-enabled improvement commands send selected source context to the configured OpenAI-compatible endpoint, so choose provider settings only when that disclosure is acceptable.

## Verification and contribution

Run the repository test suite after changing Go code or behavior:

```bash
go test ./...
```

For a focused package check, the `justfile` provides `just test ./internal/cli`; `just test-all` runs `go test ./...`. The same `justfile` provides `just build` and `just install` for the project-local binary and Go-bin symlink workflow.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Command completed. |
| 1 | Usage, validation, or runtime error. |
| 2 | Configuration could not be loaded. |
