---
"@stainless-code/verdict": patch
---

`post` survives a busy host and a long review body. A 403/429 carrying a
rate-limit signal retries within bounds — honoring `retry-after` or the reset
marker, at most three attempts, never publishing twice — and a review body past
the 65,536-character hosting limit splits into parts: the first review keeps the
footer intact, and the remaining parts post as follow-up COMMENT reviews with a
continuation header. A failed follow-up refuses typed (`post-failed`) naming the
review that already landed, and when the last permitted attempt still carries a
retryable signal the refusal adds `retry_at` (RFC 3339) so an agent can wait for
that instant instead of parsing prose.
