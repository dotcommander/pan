# Configuration and guardrails

Use this guide to locate Pan's configuration, validate it without guessing, and understand which commands can change local state.

## Inspect configuration first

Pan stores its user configuration at `pan/config.yaml` under the platform user configuration directory. A normal command seeds the file from embedded defaults on first run; `scan doctor` is the exception that uses a read-only configuration load.

```bash
./pan config print
./pan config validate
./pan config init
```

`config print` emits the effective normalized configuration. `config validate` checks a file without writing it. `config init` seeds the target file, but reports an existing file as skipped unless `--force` is supplied. The commands above are source-checked and unexecuted in this documentation update.

The effective configuration controls bounded analysis, including file, byte, node, instruction, timeout, and output limits; it also controls `clean`, `improve`, and language-server settings. Improvement run history uses `improve.state_dir`, or `pan/improve` under the platform user configuration directory when that setting is empty.

The improvement baseline executes `go test -json` in an isolated workspace. Its JSON event stream can be much larger than ordinary test output. `improve.max_stdout_bytes` defaults to 8 MiB; capture remains bounded and exceeding it terminates the owned process tree. The failure names the setting and effective byte limit. An explicitly configured smaller value remains authoritative. To allow a larger suite, set this in the user configuration:

```yaml
improve:
  max_stdout_bytes: 16777216
```

This adjusts capture capacity, not mutation authority or test eligibility.

## Choose the safe workflow

| Goal | Command | State change |
| --- | --- | --- |
| See cleanup candidates | `./pan --repo /path/to/project clean plan` | None. |
| Apply a cleanup plan without confirmation | `./pan --repo /path/to/project clean apply` | None; it is a dry run. |
| Apply the plan | `./pan --repo /path/to/project clean apply --confirm` | Creates backups and a manifest, then applies actions. |
| Find an improvement candidate | `./pan --repo /path/to/project improve recommend` | None. |
| Validate a proposal | `./pan --repo /path/to/project improve probe` | Does not apply a change. |

These commands are source-checked and unexecuted in this documentation update.

## Confirm before cleanup

`clean apply --confirm` changes the selected repository. Before it does so, Pan creates a `tar.gz` backup of every touched existing path and writes a JSON manifest. The default archive directory is `.work/archive` in that repository. Read `clean plan` before adding `--confirm`.

`clean` commands that report plans, findings, missing items, or copyable commands are read-only. The apply command is dry-run by default, so a missing `--confirm` is not an error condition.

When the cleanup walk omits eligible paths beyond `--max-depth`, every cleanup JSON envelope reports `analysis.complete: false`, the `max_depth` limit, and skipped paths. Health scores and missing-item reports then describe only inspected content; they cannot establish that omitted paths are clean or absent. Deliberate exclusions and nested repositories do not make the selected scope incomplete. Compact and evidence plan modes carry the same walk coverage.

`clean.protected_names` excludes matching file basenames from relocation, archiving, deletion, renaming, and untracking. The default is `["SKILL.md"]`, preserving skill discovery at both root and nested locations, whether tracked or untracked. Missing or empty lists inherit the embedded default, including in older configuration files. A nonempty custom list replaces the default; include `SKILL.md` when adding other protected names. It does not protect an enclosing directory from a separate cleanup action.

## Provider disclosure

`improve recommend` and `improve probe` do not apply changes. Guarded improvement preparation and refactoring use an isolated copy by default. Provider-backed improvement commands are different: a configured OpenAI-compatible endpoint can receive selected source context. Configure a provider only when that disclosure is acceptable.

| Symptom | Cause | Solution | Prevention |
| --- | --- | --- | --- |
| `config validate` reports a missing or invalid file. | The selected path does not exist or does not decode as Pan configuration. | Correct the file, or use `config init` to seed a new one. | Run `config validate` after editing configuration. |
| A cleanup appears not to have changed files. | `clean apply` was run without `--confirm`. | Review the plan, then rerun with `--confirm` only when the listed actions are acceptable. | Treat the default apply mode as a review step. |
