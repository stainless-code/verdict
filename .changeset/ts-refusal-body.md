---
"@stainless-code/verdict": patch
---

`VerdictRefusalError` now carries the whole refusal body, so `error.problems[]`, `error.problems_truncated`, `error.candidates`, and `error.retry_at` are readable from the error instead of lost.
