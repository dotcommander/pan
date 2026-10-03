# Pan documentation

Use Pan to obtain bounded source evidence before you decide where to read or review: build the local binary with `just build`, then run `./pan --repo . --format json scan overview`. The command returns a `pan/v1` envelope with snapshot-derived counts and coverage; inspect `analysis.complete`, `analysis.limits`, and `analysis.skipped` before treating a missing result as conclusive.

| Reader task | Guide |
| --- | --- |
| Inspect an unfamiliar repository | [Inspect a repository](inspect-a-repository.md) |
| Configure Pan or use guarded workflows | [Configuration and guardrails](configuration-and-guardrails.md) |
| Connect a coding agent over standard input/output | [Agent protocols](agent-protocols.md) |

## Limits before action

Pan reads source; it does not execute the selected repository. Analysis can be incomplete because of configured bounds, excluded paths, generated code, reflection, dynamic dispatch, parsing, or package-loading limits. `clean apply` is dry-run by default, and provider-enabled improvement commands can disclose selected source context to a configured OpenAI-compatible endpoint.

- [Review report JSON versions and model verdicts](review-report-format.md)
