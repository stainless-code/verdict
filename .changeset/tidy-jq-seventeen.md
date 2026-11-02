---
"@stainless-code/verdict": patch
---

Refusal paths no longer crash on jq 1.7. Envelope construction used object-literal syntax jq 1.8 accepts but 1.7 rejects, so every typed refusal (`--json` errors, oversize-fetch candidates, prompt-dump envelopes) died with exit 3 and an empty error code on runners shipping jq 1.7.x. The filters are parenthesized now and parse identically on jq 1.7 and 1.8. Minimum supported jq is 1.7 (documented in requirements); `doctor` warns on older releases via a `jq-version` row.
