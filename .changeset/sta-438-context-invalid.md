---
"@stainless-code/verdict": minor
---

A bad `--context` path now refuses `context-invalid` with a path-specific hint instead of `fetch-failed`.

**Breaking changes**

A bad `--context` input — missing, symlinked, too deep or too large, or the wrong type — now refuses `context-invalid` instead of `fetch-failed`. Scripts that matched `fetch-failed` for these cases must match `context-invalid`. Bundle-write failures still refuse `fetch-failed`. The refusal names the path as typed and, for a relative path, its absolute resolution, and its hint tells you to check each `--context` path.
