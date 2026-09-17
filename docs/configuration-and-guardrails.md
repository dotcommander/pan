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

## Provider disclosure

`improve recommend` and `improve probe` do not apply changes. Guarded improvement preparation and refactoring use an isolated copy by default. Provider-backed improvement commands are different: a configured OpenAI-compatible endpoint can receive selected source context. Configure a provider only when that disclosure is acceptable.

| Symptom | Cause | Solution | Prevention |
| --- | --- | --- | --- |
| `config validate` reports a missing or invalid file. | The selected path does not exist or does not decode as Pan configuration. | Correct the file, or use `config init` to seed a new one. | Run `config validate` after editing configuration. |
| A cleanup appears not to have changed files. | `clean apply` was run without `--confirm`. | Review the plan, then rerun with `--confirm` only when the listed actions are acceptable. | Treat the default apply mode as a review step. |
