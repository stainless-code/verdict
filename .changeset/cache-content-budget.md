---
"@stainless-code/verdict": patch
---

Bound serve cache content hashing to 16 MiB per bundle lookup, shared across artifacts, chunks, and override fragments. Bypass the cache when the content budget is exceeded.
