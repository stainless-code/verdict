---
"@stainless-code/verdict": patch
---

`examples/typescript-client/` now runs `record` (including `--dry-run`), `post --dry-run` and `config` validation end to end against the locally built binary, and CI runs every example, so a stale example fails the build.
