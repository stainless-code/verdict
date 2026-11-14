---
"@stainless-code/verdict": minor
---

Add advisory change-risk scoring (`review.json.risk`, `report --risk`,
`report --max-risk`, and `trend` risk-band rollups). `review.json` accepts an
optional `risk: {level, rationale, assessed_head}` claim validated during
`record` and persisted to `record.json` / `post.json` (with stale-head detection
via `risk-stale-head`). `report --risk` reports `claimed`, deterministic store
`floor` inputs, and `reported = max(claimed, floor)` (keeping unassessed
changes `unknown` and incomplete evidence at `>= medium`); `report --max-risk`
adds an offline fail-closed gate dimension. `trend` adds per-band outcome counts
under `by_risk` and `per_bundle[].risk`. Exposed across CLI, MCP `report`,
Go SDK `Client.Report`, JSON schemas, and TypeScript client types.
