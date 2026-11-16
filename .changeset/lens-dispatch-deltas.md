---
"@stainless-code/verdict": patch
---

Drop the per-lens findings cap and lead the dispatch with its domain. A lens
now returns every finding its evidence supports: the built-in pack declares
only `max_files: 12` (so `lenses.txt` headers read `max_files=12`, no
`max_findings=`), and all three bodies ask for "at most ~12 files" without a
return count. Aggregation already decides what survives, so the cap only
discarded evidence the lens had paid for. The shared dispatch template in
`prompts/reviewer-subagent.md` gains one leading line, `Domain: <the area this
lens owns, e.g. billing lens>`, matching the framing `prompts/review.md` and
`prompts/incremental-review.md` already ask for. `max_findings` stays a valid
pack and recipe declaration (1..20); no shipped pack sets one.

`prompts/reviewer-subagent.md` is not in the hashed prompt set, so
`prompts_hash` does not move. The pack hash moves from
`06820abbd0a6…` to `120173839a93…` and the lens hashes from
`api-contract:2aa23d13`, `perf:67c1d05a`, `test:456be8d2` to `62c9ccf8`,
`6225fd98`, `efd2eb4e`; the `fetch` goldens and the two eval literals that pin
them follow.
