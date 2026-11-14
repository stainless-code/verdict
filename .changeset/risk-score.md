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

`report --risk` also publishes `data.risk.score`, an advisory 0–100 triage
score built only from the bundle's own artifacts: `impact.json` for reach,
`full.diff` for churn, the worst open-finding category, and the weakest
open-finding rung. Each component echoes the points it earned and the weight it
carried, so every value is recomputable without a config read, and `measured`
counts the components whose inputs were read: a bundle with no `impact.json`
reads `reach.measured: false`, `reach.points: 0`, and `score.measured: 3`
instead of a silent zero. Human mode prints one `risk score:` line after
`risk floor inputs:`. The score rides `--risk` alone, so `--max-risk` keeps
gating on `reported` and a band and its score can never disagree.
