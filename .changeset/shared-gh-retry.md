---
"@stainless-code/verdict": patch
---

Share one bounded retry/backoff between `fetch`, `post`, and the provider
CLIs. `fetch` now retries GitHub's transient `internal error occurred,
please try again` response on the same 3-attempt budget `post` already used
instead of failing the run on the first attempt; plain auth and validation
failures still fail fast, and warning text and attempt limits are unchanged.
