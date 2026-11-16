---
"@stainless-code/verdict": patch
---

**No false `basis-replaced` warning** — a `review.json` body with no leading
quote block has no confidence lead to insert a Basis line under, so `record`
leaves it byte-identical and reports nothing instead of warning that it
inserted a line it did not insert.
