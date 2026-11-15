---
"@stainless-code/verdict": patch
---

`record.json` carries the author/reviewer pair: `author` (`{agent, model}` from
`author.json`, null when the bundle has none) and `review_models`, every model
name `cost.json` prices. An absent fingerprint stores null, never a human.
