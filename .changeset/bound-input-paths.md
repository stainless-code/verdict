---
"@stainless-code/verdict": patch
---

Bound every untrusted input read. `record`/`post` refuse an oversized agent
file while reading it instead of after allocating the whole document,
`verify` and the read/fetch runners cap captured child output (a capped
verify proof records a truncation marker rather than growing the process),
and the store readers (`findings.json`, `kb.jsonl`, `cost.json`, `PR.json`)
reject oversized documents through one capped JSON entry point while keeping
each store's existing refusal code.
