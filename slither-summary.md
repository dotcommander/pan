# Slither Summary

- Report: `sha256:4ae506e82bef81d04a931f281fe41765d938e3bdc086aa0801fd27868d96b081`
- Source: `git`; discovery candidates: `701`
- Selection: `155` rows, `80` ranked production files
- Ranking health: top-15 slots `15`, distinct scores `1`, saturation `0.93`
- Scoring: configured `deterministic`; actual `deterministic`; model selections `0`; deterministic selections `155`; model-error rows `0`
- Review lanes: `cli-ux`, `api-contracts`, `data-integrity`, `error-handling`, `lifecycle-concurrency`, `performance`, `test-risk`, `coupling`, `architecture`

## Start Here

| rank | file | score | confidence | actionability | evidence | verify |
| ---: | --- | ---: | --- | --- | --- | --- |
| 1 | `internal/analyze/snapshot.go` | 5 | high | inspect | content-risk, architecture-smell, unknowns, ownership, hotspot, +3 more | go test ./internal/analyze/... |
| 2 | `internal/cache/cache.go` | 5 | high | inspect | path-risk, content-risk, centrality, ownership, hotspot, +3 more | go test ./internal/cache/... |
| 3 | `internal/config/config.go` | 5 | high | inspect | path-risk, content-risk, unknowns, centrality, hotspot, +3 more | go test ./internal/config/... |
| 4 | `internal/gitworktree/worktree.go` | 5 | high | inspect | content-risk, unknowns, hotspot, size, churn, +1 more | go test ./internal/gitworktree/... |
| 5 | `internal/codemap/codemap.go` | 5 | high | inspect | content-risk, architecture-smell, centrality, ownership, hotspot, +3 more | go test ./internal/codemap/... |
| 6 | `internal/provider/client.go` | 5 | high | inspect | path-risk, content-risk, architecture-smell, sdk-dx, centrality, +5 more | go test ./internal/provider/... |
| 7 | `internal/agent/serve.go` | 5 | high | inspect | path-risk, content-risk, architecture-smell, ownership, hotspot, +3 more | go test ./internal/agent/... |
| 8 | `internal/improve/runner.go` | 5 | high | inspect | content-risk, unknowns, ownership, hotspot, size, +2 more | go test ./internal/improve/... |
| 9 | `internal/retrieval/eval.go` | 5 | high | inspect | content-risk, architecture-smell, unknowns, ownership, hotspot, +3 more | go test ./internal/retrieval/... |
| 10 | `internal/scan/risk.go` | 5 | high | inspect | content-risk, unknowns, ownership, hotspot, size, +3 more | go test ./internal/scan/... |

Skipped signals: `model_scoring:not_configured`
