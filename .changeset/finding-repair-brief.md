---
"@stainless-code/verdict": minor
---

Findings can now carry a repair brief: the affected sites, a one-sentence
outcome, an optional approach and open questions. `record` validates it against
the fetched change and keeps it across re-records, `show --json` and `fix`
print it, and it never changes a finding's signature. A malformed brief warns
and is dropped; it never refuses a review.
