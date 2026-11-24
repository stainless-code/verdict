---
"@stainless-code/verdict": minor
---

`verdict mcp` now exposes dashboard reads over the shared query layer (`internal/core/query`)
as six read-only tools — `query-bundles`, `query-findings`, `query-finding`, `query-copy`,
`query-cost`, and `query-health` — plus `verdict://` resources: static reads
(`verdict://roots`, `verdict://bundles`, `verdict://health`, `verdict://cost`) in
`resources/list`, per-bundle templates in `resources/templates/list`, and `resources/read`
returning each composed view as `application/json`. The tools answer listing, single-finding,
fix-prompt, cost, and health questions with no additional counting or finding logic;
everything resolves inside the served root, journals nothing, spawns nothing, and
writes nothing.
