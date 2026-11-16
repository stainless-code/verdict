---
"@stainless-code/verdict": patch
---

**Coverage range shape** — `next --coverage` refuses an array-valued
`coverage[].ranges` with the same `bad-review-input` problem `record` reports,
instead of printing a silent 0% attestation.
