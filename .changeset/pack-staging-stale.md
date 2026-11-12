---
"@stainless-code/verdict": patch
---

`verdict pack add` refuses a crashed re-add's leftover staging or backup sibling as
`pack-staging-stale` when it cannot sweep it, naming the path to delete instead of
suggesting a `pack.json` repair. The loader still ignores those dotted siblings, so
`pack list` and `fetch` are unaffected.
