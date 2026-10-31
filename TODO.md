# Current task

Implement the four efficiency recommendations authorized by Gary. Preserve ambient changes; no commits or installation.

- [x] Scope semantic Go package loading to captured packages while retaining type resolution and fallback semantics.
- [x] Prepare risk path matchers once per scan without changing matching or scoring.
- [x] Stream cache-status content validation without retaining source bytes; preserve freshness checks.
- [x] Reuse task candidate term matches without changing selection or disclosures.
- [x] After the complete slice, run one relevant build, focused tests for the four owning packages, and consolidated diff review.

Verification: `GOWORK=off GOPROXY=off go build -o .work/pan-optimize ./cmd/pan` passed. `GOWORK=off GOPROXY=off go test ./internal/analyze ./internal/scan ./internal/cache ./internal/retrieval` passed all four packages. Consolidated diff review and whitespace check passed. Initial build caught a return-arity error in the new scoring helper; fixed before the passing build and tests. Runtime speedups remain unmeasured; no installation or commit performed.

Durable execution: task `29774fcd`; FUI thread `thr_01M69GVAT3CTNAPNZXQ115M4HT`, approved scope post `pst_01M69GVGDMENXFDP3WTF2QTZRX`.
