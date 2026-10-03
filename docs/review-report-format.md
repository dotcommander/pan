# Review report JSON

`pan review report --json` emits `pan.review-report/v1` for deterministic reports and `pan.review-report/v2` when model verdicts are present. Current readers accept both versions, reject unknown fields and invalid verdicts, and reject verdict annotations in v1. Existing v1 bytes and report identities retain their original contract.

Deterministic filtering, scoring, cull lanes and top selection finish before any model request. Only retained `kept` rows are sent. Model output is a separate optional `model_verdict` object with `status` of `success`, `inconclusive` or `error`. A successful verdict carries an integer `score` from 1 to 5 and optional `summary` and `reasons`; unsuccessful verdicts carry a safe `detail` and no score. Deterministic row scores, reasons, evidence identities and lanes stay unchanged.

Successful verdicts sort by descending model score within their existing kept positions. Ties retain deterministic order, and unsuccessful verdicts and other lanes retain their positions. Rank, rationale and existing cull ledger projections follow the resulting order without reculling. A v2 report identity incorporates verdict annotations and row order. Provider or malformed-response failures produce v2 error annotations with deterministic fallback; caller cancellation remains terminal.

Verdict caches use a separate ranking-contract version and deterministic input identity, captured source hashes and effective prompt. Existing score caches are ignored. Responses must be JSON arrays with unique request-local indices; fenced JSON, unknown fields, noninteger scores, duplicate indices and out-of-range indices fail safely. Missing judgments and out-of-range scores remain inconclusive and are never cached. No model request uses an object-only response format.

Consumers should accept v1 and v2 explicitly, keep deterministic scores separate from model scores, and avoid treating a failure annotation as successful model ranking. Markdown labels model ranking as disabled, successful, partial or unavailable.
