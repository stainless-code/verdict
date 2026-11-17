---
"@stainless-code/verdict": minor
---

`drain --post` answers the waiting review threads from a validated `drain.replies.json`. GitHub and GitLab post one marked reply per draft and append `replies.json` under a lock after each write; `withdraw` also resolves its thread. Secret-shaped bodies refuse before any write, stale threads are skipped, and a failed write records `failed[]` and refuses `drain-incomplete` so the same command can finish the remaining replies.
