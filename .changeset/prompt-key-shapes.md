---
"@stainless-code/verdict": patch
---

Repair `in_bounds` to `in-bounds` on a review comment, beside the existing
`inBounds`/`rung`/`file` aliases.

The benchmark's tool-free prompt keeps the `review.json` coverage entry shape
(`{"file": …, "ranges": …}`) it used to replace away with the rest of the submit
step, and a bench test holds that derived prompt to every key shape the shipped
prompt publishes, so the two cannot drift apart again.
