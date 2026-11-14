---
"@stainless-code/verdict": minor
---

Add per-rule `recalled_in`, `cited_in`, `exposure`, and `acceptance` columns to `learn --status` (text and `--json`), reusing the bundle overlap join and reporting missing recall manifests as `unmeasured`/`null`.
