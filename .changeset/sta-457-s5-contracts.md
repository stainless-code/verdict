---
"@stainless-code/verdict": minor
---

Two-finder executions now render which finders completed, the cross-check counts, each finding's origin and checker standing, and quoted refutation evidence. `record`, `post`, `report` and their MCP tools recompute cross-check eligibility from the bound artifacts, so a missing or forged stored stamp cannot replace them.

**Breaking (SDK wire):** the report cost token fields (`input_tokens`, `output_tokens`, `total_tokens`, `cached_input_tokens`, and those under `by_tier`) can be `null`, meaning unknown usage, never a measured zero. The generated TypeScript types are nullable; clients must handle `null`. A rollup that could not price a launched phase reports `est_usd: null` instead of a partial total.
