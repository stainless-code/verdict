---
"@stainless-code/verdict": patch
---

Repair `in_bounds` to `in-bounds` on a review comment, beside the existing
`inBounds`/`rung`/`file` aliases.

The benchmark's tool-free prompt keeps the `review.json` coverage entry shape
(`{"file": …, "ranges": …}`) it used to replace away with the rest of the submit
step. A bench test pins four key shapes — the comment keys `rigor` and
`in-bounds`, and the coverage entry's `file` and `ranges` — and fails when the
derived prompt drops one, or spells one from the transform's own copy instead of
the shipped prompt.
