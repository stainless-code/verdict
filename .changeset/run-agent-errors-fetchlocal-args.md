---
"@stainless-code/verdict": patch
---

`verdict run` no longer reports every agent failure as `review-insufficient`. When every finder fails, the run refuses with the agent's own code (`agent-failed` with its exit status, or `no-review-artifact`) and a bounded, credential-scrubbed tail of its output, so an HTTP 402 or a refused write is visible. A partial two-finder run keeps `review-insufficient` but names the failed finder's cause. The `claude` permission hint now prints for two-finder launches too.

**Behavior change:** `fetch-local` refuses a bare word before `--` as a usage error instead of treating it as a path filter; path filters stay `-- <paths>`.

`verify`'s recorded output tail is now scrubbed before it is cut, with terminal escapes removed first, so a credential split by the 4,000-byte cut or an escape sequence no longer leaves a readable piece in `patch.json`.
