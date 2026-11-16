---
"@stainless-code/verdict": patch
---

**Optional `schema_version`** — `record` no longer warns that a valid
`review.json` omits `schema_version`, which it reads as version 1. A present
value other than `"1"` still refuses.
