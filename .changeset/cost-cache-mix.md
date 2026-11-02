---
"@stainless-code/verdict": patch
---

Cached vs fresh input-token split in `cost.json`. Per-model entries gain optional `cache_read_input_tokens` and `cache_creation_input_tokens` (subsets of `input_tokens`, absent/`null` reads unknown); `report --cost` rolls them into a top-level `cached_input_tokens` scalar with totals unchanged.
