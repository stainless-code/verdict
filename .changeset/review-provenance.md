---
"@stainless-code/verdict": patch
---

Review provenance on `post.json`/`record.json`. Both records gain additive `tool_version`, `prompts_hash` (sha256 over the sorted prompt-set files plus contents), and `model` (from the cost record when present); `calibrate` and `trend` surface them so mixed-version histories read as mixed.
