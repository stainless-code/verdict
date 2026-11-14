---
"@stainless-code/verdict": minor
---

Rank the local bundles into an advisory triage queue. `risk <owner/repo>
[--status open|all] [--limit <n>]` reads every bundle under `.verdict/state`
and prints one row per bundle — its recomputed band, the 0-100 score
(`report --risk`'s reach 35 / churn 25 / category 25 / rung 15, computed from
the bundle's own `impact.json`, `full.diff`, and open findings), and whether it
needs a human — sorted by band, then score, then bundle name. `--status open`
(the default) keeps host-open and local bundles and names the merged and closed
ones it skipped; `--status all` keeps everything. `--limit` (default 50, max 200) bounds the rows and carries the flattened receipt while the tallies keep
describing the whole queue. A bundle with no `findings.json` is queued, never
dropped, and a missing or unusable `impact.json` reads reach unmeasured at zero
points with a counted warning, so an unindexed bundle never looks like a clean
zero. Pure read: no host call, no index build, nothing written. The `data`
object is described by `$defs/riskData` in the generated envelope schema
(additive: `riskData`, `riskBands`, `riskRow`, plus the receipt keys every
capped list publishes), the TypeScript client gains `client.risk()`, the `risk`
MCP tool serves the same data read-only, and the Go facade gains `Client.Risk`.
Gating one bundle stays `report --max-risk`.
