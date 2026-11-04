---
"@stainless-code/verdict": patch
---

`verdict post` on a PR that was never fetched now refuses with the typed
`missing-bundle` error naming the fetch command, instead of failing internally
on its first write.
