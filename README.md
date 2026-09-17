# Pan

Pan is a local Go CLI that gives coding agents bounded repository evidence: build it with `just build`, then run `./pan --repo . --format json scan overview` to see a `pan/v1` report with repository counts and analysis coverage. The report may be incomplete, so its `analysis.limits` and `analysis.skipped` fields matter before you treat missing evidence as a conclusion.

| If you need to… | Start with… |
| --- | --- |
| Find or explain relevant code | `context` |
| Trace execution, dependencies, routes, or pipelines | `flow` |
| Inspect repository structure, risks, or trusted checks | `scan` |
| Build an evidence-backed review packet | `review` |
| Plan cleanup or a guarded improvement | `clean` or `improve` |
| Integrate Pan with an agent | `agent schema` |

## First use

**Prerequisites:** this source checkout requires Go 1.25 and `just`. `just build` writes the project-local `./pan` binary. The first analysis command creates a user configuration file from embedded defaults if one does not already exist.

```bash
just build
./pan --repo . --format json scan overview
```

The second command emits a `pan/v1` JSON envelope. Its `result` contains `files`, `symbols`, `edges`, languages, and other snapshot-derived counts; its `analysis` object says whether the snapshot is complete and identifies any bounds or skipped paths. Pan builds that report from a bounded source snapshot; it does not run the selected repository's code.

To check whether analysis coverage is sufficient before relying on a later result, use this source-checked, unexecuted variation:

```bash
./pan --repo . scan doctor
```

## Configuration and state

Pan stores its user configuration at `pan/config.yaml` under the platform user configuration directory. On a normal command's first run, Pan seeds that file from embedded defaults. `scan doctor` uses a read-only configuration load, so it can report configuration state without creating a missing file.

```bash
./pan config print
./pan config validate
./pan config init
```

`config init` reports an existing file as skipped unless you add `--force`. Improvement run history uses the configured `improve.state_dir`; when unset, it resolves to `pan/improve` under the same platform user configuration directory. These commands are source-checked, not executed in this documentation update.

## What Pan does not do

Pan analyzes source; it does not execute the target repository. Bounds, exclusions, generated code, reflection, dynamic dispatch, parsing, and package-loading limits can leave evidence incomplete. An absent item in an incomplete report is not proof that the item does not exist.

## Capability reference

| Command group | What it provides |
| --- | --- |
| `context` | Bounded code retrieval, repository maps, and task-oriented context. |
| `flow` | Execution, dependency, route, and pipeline evidence. |
| `scan` | Repository and symbol inventory, risk signals, public-surface and effect reports, Git evidence, analyzer health, and trusted in-process checks. |
| `review` | Structured audit packets and reports. |
| `clean` | Cleanup planning, findings, completeness gaps, and guarded application. |
| `improve` | Recommendations, coverage census, proposal probing, guarded preparation and refactoring, history statistics, and JSONL export. |
| `agent` | A sequential JSONL protocol, a JSON-RPC 2.0 NDJSON service, and a machine-readable schema. |
| `cache` and `config` | Incremental-analysis cache lifecycle and configuration bootstrap, validation, and display. |

`scan checks` runs built-in, in-process checks; it does not execute repository-provided commands. Discover its checks or select `skill-contract` explicitly with these source-checked, unexecuted commands:

```bash
./pan scan checks --list
./pan scan checks --check skill-contract path/to/skill
```

`skill-contract` checks a target `SKILL.md` file or containing directory. Findings are reported in the result; an invalid selector or an execution failure returns a nonzero exit status.

## Guarded operations

Read the cleanup plan before any write:

```bash
./pan --repo /path/to/project clean plan
./pan --repo /path/to/project clean apply
```

`clean apply` is a dry run unless `--confirm` is present. With `--confirm`, Pan creates a `tar.gz` backup of every touched existing path and writes a JSON manifest before applying actions. The default archive directory is `.work/archive` in the selected repository.

`improve recommend` and `improve probe` do not apply a change. Guarded improvement preparation and refactoring use an isolated copy by default. Provider-enabled improvement commands can send selected source context to a configured OpenAI-compatible endpoint; configure a provider only when that disclosure is acceptable.

## Verification and contribution

The `justfile` defines focused test and vet commands plus full-suite targets. These commands are source-checked, not executed in this documentation update:

```bash
just test ./internal/cli
just vet ./internal/cli
just test-all
just vet-all
```

Use `just build` to build the project-local binary. `just qa` runs formatting verification, repository-wide vetting, the full test suite, and a build.

## Limits

Results are bounded by configuration, and Pan reports analysis coverage with each snapshot-derived result. Review `analysis.complete`, `analysis.limits`, and `analysis.skipped` before making an absence claim. Use `pan <command> --help` to inspect a command's arguments and side effects before combining modes or enabling a guarded action.
