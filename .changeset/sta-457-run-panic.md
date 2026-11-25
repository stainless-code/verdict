---
"@stainless-code/verdict": patch
---

`verdict run` no longer panics when a two-finder launch reaches its response tail without an execution marker. The response now carries null counts, reports `partial`, names the missing execution as a phase failure, and refuses `agent-record-failed` with the remedy when no earlier phase refused first.
