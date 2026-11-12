---
"@stainless-code/verdict": patch
---

A batch of small fixes from recent review rounds:

- An agent that exits non-zero just before its time budget runs out is now reported `agent-failed`, not
  `agent-timeout`. The watchdog records its own budget kill instead of comparing the clock afterwards.
- MCP refusal results carry the refusal's `warnings`, both as `warn:` lines in the text and as a `warnings` field in
  the structured result.
- Human-mode output prints a refusal's warnings as `warn:` lines before `error:`, so `pack list` no longer hides a
  stray-file warning when it refuses.
- The `index-capped` refusal names the bytes it measured. Its hint no longer suggests raising a cap that cannot be
  configured.
- `index status` points at the query, not at `index build`, once HEAD is indexed.
- Reading a crashed agent's partial review can no longer block on a FIFO.
