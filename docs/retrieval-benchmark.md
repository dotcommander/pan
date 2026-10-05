# Retrieval benchmarks

`pan bench retrieval` scores Pan's retrieval against issue-to-file ground truth. It takes two local inputs: a SWE-bench-style JSONL dataset (`instance_id`, `repo`, `base_commit`, `problem_statement`, `patch`) and a directory of local git mirrors named `org__name`. For every instance it checks out `base_commit` with a read-only `git archive` extraction under the work directory, builds a bounded snapshot, runs the retrieval policy on the problem statement, and scores the resulting ranked paths against the gold files named in the patch. A deterministic BM25 baseline over captured source and path tokens is scored the same way.

```bash
./pan bench retrieval --dataset dataset.jsonl --mirrors ~/mirrors --repo acme/widget --limit 100
```

The result is a `pan.retrieval-bench/v1` envelope. `systems` carries per-system aggregates (recall@1/5/10/20, MRR, NDCG@10) for `pan` and `bm25`; `rows` carries bounded per-instance detail; `skipped` records every instance the run could not score, with reasons such as `mirror not found` or `checkout failed`. Skips are part of the contract: an unscored instance is never folded into an aggregate.

Properties to rely on:

- **Local and read-only.** The dataset and mirrors are local files; mirrors are only read through `git archive` (1 GiB stream cap). Pan never downloads datasets and never executes repository code.
- **Deterministic.** Reports carry no timestamps; an unchanged dataset, mirror, and analyzer revision produce an identical report. Gold paths come from `--- a/` / `+++ b/` patch headers, including deleted files.
- **Bounded.** Problem statements are capped at 64 KiB per case (`request_truncated` marks the row), detail rows are capped by `--top-rows`, and per-instance checkouts are created under the work directory (default `pan/bench` in the user cache directory) and removed after scoring.

Use `--repository` (repeatable) to restrict the run, `--policy structural-reference-graph/v1` to compare policies, and `--token-budget` to control the retrieval packet size under evaluation.
