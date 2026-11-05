---
"@stainless-code/verdict": minor
---

`verdict record` now repairs the mechanical shapes weak models emit instead
of refusing them. Before checking, `NormalizeReview` unwraps one
whole-document code fence, drops prose around exactly one balanced JSON
object, wraps a scalar where a string array belongs, renames the
`inBounds`/`rung`/`file` aliases, folds rigor synonyms and enum case, and
removes a BOM, an explicit null on an optional key, or an exact-duplicate
comment. Every rewrite is a warning with its own code, listed in
`record.json`'s `repairs[]`; `--strict`, `REVIEW_STRICT_INPUT=1`, or
`"strict_input": true` in `controls.json` turns each into an error.
`record --dry-run` returns the exact stored object as `data.normalized`
plus `data.repairs`, and never rewrites `review.json` on disk. The CLI also
owns the body's `> Basis:` line, replacing a disagreeing line (or inserting
a missing one) with the computed counts and reporting `basis-replaced`.
Unset rigor records as `said` with `rigor_source: default`, and length and
numeric bounds now refuse as `out-of-range` instead of `bad-enum`.
`pkg/verdict.NormalizeReview` exposes the pure, idempotent primitive.
