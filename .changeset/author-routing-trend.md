---
"@stainless-code/verdict": patch
---

`trend` groups outcomes by the author/reviewer pair: per-bundle rows carry
`author` and `review_models` from `record.json`, and `by_pair` tallies each
`<author>/<reviewer>` combination, with `unknown` for what a bundle never
recorded.
